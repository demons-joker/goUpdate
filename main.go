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
	"runtime"
	"strconv"
	"time"

	"github.com/kardianos/service"
)

var logger service.Logger

type myProgram struct{}

type Config struct {
	ServerURL      string `json:"serverURL"`
	PollInterval   string `json:"pollInterval"`
	CurrentVersion int    `json:"currentVersion"`
}

var (
	serverURL    string
	localDir     string
	logFilePath  string
	resourcesDir string
	pollInterval time.Duration
	config       *Config
)

type FileInfo struct {
	Name          string `json:"Name"`
	Size          int    `json:"Size"`
	MD5           string `json:"MD5"`
	Time          int64  `json:"Time"`
	Path          string `json:"Path"`
	Version       int    `json:"Version"`
	MinVersion    int    `json:"MinVersion"`
	EnabledUpdate int    `json:"EnabledUpdate"`
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

	// 生产环境获取可执行文件的绝对路径-------start
	configPath := "config.json"

	execPath, err := os.Executable()
	if err != nil {
		panic(err)
	}
	execDir := filepath.Dir(execPath)

	// 构造配置文件的绝对路径
	configAbsPath := filepath.Join(execDir, configPath)

	// 生产环境获取可执行文件的绝对路径-------end

	//本地测试用下面这个配置文件-------------start
	// configAbsPath := "./config.json"
	// execDir := "./"
	//-----end--------------------------

	config = &Config{}
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
	if runtime.GOOS == "darwin" {
		resourcesDir = "../../Resources/exts/preload"
	} else {
		resourcesDir = "./resources/exts/preload"
	}
	localDir = filepath.Join(execDir, resourcesDir)
	logFilePath = filepath.Join(execDir, "update.log")
	pollDuration, err := time.ParseDuration(config.PollInterval)
	if err != nil {
		return fmt.Errorf("failed to parse poll interval: %w", err)
	}
	pollInterval = pollDuration

	return nil
}

/**
*计算MD5
**/
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("服务器返回错误状态码: %d", resp.StatusCode)
	}

	out, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("无法创建本地文件: %v", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("内容复制失败: %v", err)
	}

	if err := out.Sync(); err != nil {
		return fmt.Errorf("文件同步失败: %v", err)
	}

	return nil
}

// 下载更新preload.json文件
func updatePreloadJSONIfNeeded() error {
	localPreloadPath := filepath.Join(localDir, "preload.json")
	err := downloadFile(serverURL, localPreloadPath)
	if err != nil {
		appendToLog("failed to download preload.json")
	}

	return compareAndUpdateFiles()
}

// 对比更新bin文件
func compareAndUpdateFiles() error {
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

	for _, remoteFileInfo := range remoteFileInfos {
		if config.CurrentVersion >= remoteFileInfo.MinVersion {
			if remoteFileInfo.EnabledUpdate == 1 {
				// log.Printf("远程文件版本 %d,当前客户端版本 %d,远程最小起更版本 %d,更新至 %d\n", remoteFileInfo.Version, config.CurrentVersion, remoteFileInfo.MinVersion, remoteFileInfo.Version)
				err := updateFile(remoteFileInfo.Name, remoteFileInfo)
				if err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func updateFile(fileName string, fileInfo FileInfo) error {
	localFilePath := filepath.Join(localDir, fileName)
	localMD5, err := calculateMD5(localFilePath)
	if err != nil {
		appendToLog("failed to open " + localFilePath + "err :" + err.Error())
		return fmt.Errorf("failed to open %s: %w", localFilePath, err)
	}
	if localMD5 == fileInfo.MD5 {
		return nil
	}
	tempFilePath := localFilePath + ".tmp"
	appendToLog("tempFilePath url " + tempFilePath)
	err = downloadFile(fileInfo.Path, tempFilePath)
	if err != nil {
		appendToLog("failed to download " + localFilePath + "err :" + err.Error())
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
