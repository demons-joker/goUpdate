package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kardianos/service"
)

var logger service.Logger

type myProgram struct{}

type Config struct {
	ServerURL    string `json:"serverURL"`
	PollInterval string `json:"pollInterval"`
}

var (
	serverURL    string
	localDir     string
	logFilePath  string
	pollInterval time.Duration
)

type FileInfo struct {
	Name    string `json:"Name"`
	Size    int    `json:"Size"`
	MD5     string `json:"MD5"`
	Time    int64  `json:"Time"`
	Path    string `json:"Path"`
	Version int    `json:"Version"`
}

func (p *myProgram) Start(s service.Service) error {
	// 启动服务时执行的操作
	go p.run()
	return nil
}

func (p *myProgram) run() {

	appendToLog("Service running...")
	err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	for {
		log.Printf("checking for updates...")
		appendToLog("checking for updates...")
		err := updatePreloadJSONIfNeeded()
		if err != nil {
			log.Printf("Update failed: %v\n", err)
			appendToLog("Update failed with an error")
		}

		time.Sleep(pollInterval)
	}
}

func (p *myProgram) Stop(s service.Service) error {
	// 停止服务时执行的操作
	appendToLog("Service stopped.")
	return nil
}

func loadConfig() error {

	configPath := "config.json"

	// 生产环境获取可执行文件的绝对路径-------start
	execPath, err := os.Executable()
	if err != nil {
		panic(err)
	}
	execDir := filepath.Dir(execPath)

	// 构造配置文件的绝对路径
	configAbsPath := filepath.Join(execDir, configPath)
	// 生产环境获取可执行文件的绝对路径-------end

	//本地测试用下面这个配置文件
	// configAbsPath := "./config.json"

	config := &Config{}
	file, err := os.Open(configAbsPath)
	if err != nil {
		appendToLog("failed to open config file" + configAbsPath)
		return fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(config); err != nil {
		return fmt.Errorf("failed to decode config file: %w", err)
	}

	serverURL = config.ServerURL
	localDir = filepath.Join(execDir, "./resources/exts/preload")
	logFilePath = filepath.Join(execDir, "update.log")
	pollDuration, err := time.ParseDuration(config.PollInterval)
	if err != nil {
		return fmt.Errorf("failed to parse poll interval: %w", err)
	}
	pollInterval = pollDuration

	return nil
}

func calculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := md5.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func downloadFile(url, filePath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("无法下载文件: %v", err)
	}
	defer resp.Body.Close()

	out, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("无法创建本地文件: %v", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func updatePreloadJSONIfNeeded() error {
	localPreloadPath := filepath.Join(localDir, "preload.json")
	_, err := os.Stat(localPreloadPath)
	if err != nil && !os.IsNotExist(err) {
		appendToLog("failed to stat local preload.json")
		return fmt.Errorf("failed to stat local preload.json: %w", err)
	}

	if os.IsNotExist(err) {
		log.Printf("preload.json 不存在，正在从远端拉取...\n")
		err = downloadFile(serverURL, localPreloadPath)
		if err != nil {
			appendToLog("failed to download preload.json")
			return fmt.Errorf("failed to download preload.json: %w", err)
		}
		return compareAndUpdateFiles(localPreloadPath)
	}

	return compareAndUpdateFiles(localPreloadPath)
}

func compareAndUpdateFiles(localPreloadPath string) error {
	resp, err := http.Get(serverURL)
	if err != nil {
		appendToLog("failed to fetch versions")
		return fmt.Errorf("failed to fetch versions: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		appendToLog("failed to read response body")
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var remoteFileInfos []FileInfo
	err = json.Unmarshal(responseBody, &remoteFileInfos)
	if err != nil {
		appendToLog("failed to decode versions")
		return fmt.Errorf("failed to decode versions: %w", err)
	}

	localFileInfos, err := readLocalPreloadJSON(localPreloadPath)
	if err != nil {
		return err
	}

	for _, remoteFileInfo := range remoteFileInfos {
		for i, localFileInfo := range localFileInfos {
			if localFileInfo.Name == remoteFileInfo.Name {
				if localFileInfo.Version < remoteFileInfo.Version {
					log.Printf("发现新版本文件: %s, 从版本 %d 更新至 %d\n", localFileInfo.Name, localFileInfo.Version, remoteFileInfo.Version)
					err := updateFile(localFileInfo.Name, remoteFileInfo)
					if err != nil {
						return err
					}
					localFileInfos[i].Version = remoteFileInfo.Version
					localFileInfos[i].MD5 = remoteFileInfo.MD5
				}
				break
			}
		}
	}

	// 更新本地 preload.json
	err = writeLocalPreloadJSON(localPreloadPath, localFileInfos)
	if err != nil {
		return err
	}

	return nil
}

func readLocalPreloadJSON(path string) ([]FileInfo, error) {
	respBody, err := ioutil.ReadFile(path)
	if err != nil {
		appendToLog("failed to read local preload.json")
		return nil, fmt.Errorf("failed to read preload.json: %w", err)
	}

	var fileInfos []FileInfo
	err = json.Unmarshal(respBody, &fileInfos)
	if err != nil {
		appendToLog("failed to decode local preload.json")
		return nil, fmt.Errorf("failed to decode preload.json: %w", err)
	}

	return fileInfos, nil
}

func writeLocalPreloadJSON(path string, fileInfos []FileInfo) error {
	data, err := json.MarshalIndent(fileInfos, "", "  ")
	if err != nil {
		appendToLog("failed to marshal local preload.json")
		return fmt.Errorf("failed to marshal preload.json: %w", err)
	}

	err = ioutil.WriteFile(path, data, 0644)
	if err != nil {
		appendToLog("failed to write local preload.json")
		return fmt.Errorf("failed to write preload.json: %w", err)
	}

	return nil
}

func updateFile(fileName string, fileInfo FileInfo) error {
	localFilePath := filepath.Join(localDir, fileName+".bin")
	tempFilePath := localFilePath + ".tmp"

	err := downloadFile(fileInfo.Path, tempFilePath)
	if err != nil {
		appendToLog("failed to download " + localFilePath)
		return fmt.Errorf("failed to download %s: %w", fileName, err)
	}

	actualMD5, err := calculateMD5(tempFilePath)
	if err != nil {
		os.Remove(tempFilePath)
		appendToLog("failed to calculate MD5 " + localFilePath)
		return fmt.Errorf("failed to calculate MD5 for %s: %w", fileName, err)
	}

	if actualMD5 != fileInfo.MD5 {
		os.Remove(tempFilePath)
		appendToLog("MD5 verification failed for " + fileName + ". Expected: " + fileInfo.MD5 + ", Actual:" + actualMD5)
		return fmt.Errorf("MD5 verification failed for %s. Expected: %s, Actual: %s", fileName, fileInfo.MD5, actualMD5)
	}

	err = os.Rename(tempFilePath, localFilePath)
	if err != nil {
		appendToLog("failed to replace " + fileName)
		return fmt.Errorf("failed to replace %s: %w", fileName, err)
	}

	appendToLog("更新文件: " + fileName + " 至版本 " + strconv.Itoa(fileInfo.Version))
	return nil
}

func appendToLog(message string) {
	file, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file: %v\n", err)
		return
	}
	defer file.Close()

	logger := log.New(file, "", log.LstdFlags)
	logger.Println(message)
}

func main() {

	svcConfig := &service.Config{
		Name:        "DjsPreloadUpdate",
		DisplayName: "DJS Preload Update Service",
		Description: "DJS Preload Update Service",
	}

	prg := &myProgram{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}
	logger, err = s.Logger(nil)
	if err != nil {
		log.Fatal(err)
	}
	err = s.Run()
	if err != nil {
		logger.Error(err)
	}
}
