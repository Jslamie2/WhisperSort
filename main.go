package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Define the size of our sorting queue
const queueSize = 500 // ⬅️ INCREASED QUEUE SIZE

// Channel for the sorting queue
var SorterQueue = make(chan string, queueSize)

func printQueue() {

	for i := range SorterQueue {
		fmt.Println(i)
		fmt.Println("running function")
	}
}

type AppConfig struct {
	IsProjectMode bool
	ProjectPath   string
}

var currentConfig = AppConfig{
	IsProjectMode: false,
	ProjectPath:   "/home/jgeeking/Downloadsss",
}

var fileCategories = map[string]string{

	"pdf": "Documents", "doc": "Documents", "docx": "Documents", "txt": "Documents", "csv": "Documents", "pptx": "Documents", "xlsx": "Documents", "rtf": "Documents",

	"jpg": "Images", "jpeg": "Images", "png": "Images", "gif": "Images", "bmp": "Images", "tiff": "Images", "webp": "Images", "svg": "Images",

	"mp4": "Videos", "mov": "Videos", "avi": "Videos", "mkv": "Videos", "webm": "Videos",

	"mp3": "Audio", "wav": "Audio", "flac": "Audio", "ogg": "Audio",

	"zip": "Archives", "rar": "Archives", "7z": "Archives", "tar": "Archives", "gz": "Archives",

	"exe": "Programs", "msi": "Programs", "dmg": "Programs", "deb": "Programs", "go": "Code", "py": "Code", "js": "Code",
	"sh": "Code",
}

var ErrFileBusy = fmt.Errorf("file is busy or locked")

func getDownloadPath() string {
	home, err := os.UserHomeDir()
	fmt.Println(home)
	if err != nil {
		log.Fatalf("Error finding home directory: %v", err)
	}
	path := filepath.Join(home, "Downloads")
	fmt.Println(path)

	entries, err := os.ReadDir(path)
	if err != nil {
		log.Fatal(err)
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() { // only count files
			count++
		}
	}
	fmt.Println("Number of files:", count)

	fmt.Println("Number of files:", count)
	switch runtime.GOOS {
	case "windows":

		return filepath.Join(home, "Downloads")
	case "darwin", "linux":
		directory := filepath.Join(home, "Downloads")
		fmt.Println(directory)
		return filepath.Join(home, "Downloads")
	default:
		log.Fatalf("Unsupported operating system: %s", runtime.GOOS)
		return ""
	}
}

func getActivePath(config AppConfig) string {
	if config.IsProjectMode && config.ProjectPath != "" {
		return config.ProjectPath
	}
	return getDownloadPath()
}

func categorizeFile(filename string) (string, bool) {
	ext := filepath.Ext(filename)
	if ext == "" {
		return "", false
	}
	cleanExt := strings.ToLower(strings.TrimPrefix(ext, "."))

	category, exists := fileCategories[cleanExt]
	if !exists {
		return "Others", true
	}
	return category, true
}

func handleFileMove(filePath string, config AppConfig) error {
	filename := filepath.Base(filePath)
	if strings.HasPrefix(filename, ".") || strings.HasSuffix(filename, "~") || strings.Contains(filename, ".download") {
		return nil
	}

	category, ok := categorizeFile(filename)
	fmt.Println(ok)
	if !ok {
		return nil
	}

	baseDir := filepath.Dir(filePath)
	if config.IsProjectMode {
		baseDir = config.ProjectPath
	}

	destDir := filepath.Join(baseDir, category)
	destPath := filepath.Join(destDir, filename)

	if err := os.MkdirAll(destDir, fs.ModePerm); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", destDir, err)
	}

	err := os.Rename(filePath, destPath)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "no such file or directory") {
			return nil
		}
		if strings.Contains(errStr, "resource busy") ||
			strings.Contains(errStr, "access is denied") ||
			strings.Contains(errStr, "The process cannot access the file") ||
			(runtime.GOOS == "linux" && strings.Contains(errStr, "device or resource busy")) ||
			(runtime.GOOS == "windows" && strings.Contains(errStr, "The process cannot access the file because it is being used by another process")) {
			return ErrFileBusy
		}

		return fmt.Errorf("move failed: %w", err)
	}

	log.Printf("✅ Sorted: %s -> %s", filename, destPath)
	return nil
}

func SorterWorker(config AppConfig) {
	for filePath := range SorterQueue {

		time.Sleep(1 * time.Second)

		maxRetries := 5
		retryDelay := 2 * time.Second

		for attempt := 1; attempt <= maxRetries; attempt++ {
			err := handleFileMove(filePath, config)

			if err == nil {
				break
			}

			if err == ErrFileBusy {
				log.Printf("⏳ Retry #%d for %s: File busy. Waiting %v...", attempt, filepath.Base(filePath), retryDelay)
				time.Sleep(retryDelay)
				continue
			}

			log.Printf("❌ Fatal sort error for %s: %v", filepath.Base(filePath), err)
			break
		}
	}
}

func main() {
	go printQueue()

	go SorterWorker(currentConfig)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	watchPath := getActivePath(currentConfig)

	log.Printf("👀 Watching folder: %s (Project Mode: %t)", watchPath, currentConfig.IsProjectMode)

	if _, err := os.Stat(watchPath); os.IsNotExist(err) {
		log.Fatalf("Watch folder not found at: %s. Please create it or update the config.", watchPath)
	}

	err = watcher.Add(watchPath)
	if err != nil {
		log.Fatal(err)
	}

	done := make(chan bool)
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
					filename := filepath.Base(event.Name)
					if strings.HasPrefix(filename, ".") ||
						strings.HasSuffix(filename, "~") ||
						strings.HasSuffix(filename, ".part") || // Common partial download extension
						strings.HasSuffix(filename, ".crdownload") || // Chrome's partial download extension
						strings.Contains(filename, ".download") { // Generic download pattern

						continue // Skip queuing this event
					}

					select {
					case SorterQueue <- event.Name:
						log.Printf("➕ Queued: %s for sorting.", filepath.Base(event.Name))
					default:
						log.Printf("⚠️ Queue is full. Dropping legitimate event for %s.", filepath.Base(event.Name))
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("Error watching files:", err)
			}
		}
	}()

	<-done
}
