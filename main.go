package main

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v2"
)

const layout = "2006:01:02 15:04:05"
const defaultConfigPath = "config.yaml"

var picTypes = map[string]bool{
	"jpg":  true,
	"jpeg": true,
	"png":  true,
	"gif":  true,
	"bmp":  true,
}

var AudioTypes = map[string]bool{
	"mp3":  true,
	"flac": false,
	"wav":  false,
}

var videoTypes = map[string]bool{
	"mp4": true,
	"mov": true,
	"avi": true,
	"wmv": true,
	"mkv": true,
	"rm":  true,
	"f4v": true,
	"flv": true,
	"swf": true,
}

type configFile struct {
	ModelMap map[string]string `yaml:"model_map"`
}

// time regex to time layout
var regexTime = map[string]string{
	`\d{8}_\d{6}`:                           "20060102_150405",
	`\d{4}-\d{2}-\d{2} \d{2}\.\d{2}\.\d{2}`: "2006-01-02 15.04.05",
}

type Config struct {
	Source      string
	Destination string
	Dry         bool
	Rename      bool
	NoSkip      bool
	OverWrite   bool
	Yes         bool
	Together    bool
	Mode        string
	ConfigPath  string
}

var c = Config{}
var y = configFile{}

var fileCommand = &cli.Command{
	Name:  "file",
	Usage: "copy or move file",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:        "dry",
			Destination: &c.Dry,
			Usage:       "dry run",
		},
		&cli.StringFlag{
			Name:        "source",
			Aliases:     []string{"s"},
			Destination: &c.Source,
			Usage:       "source directory",
			Required:    true,
		},
		&cli.StringFlag{
			Name:        "dest",
			Aliases:     []string{"d"},
			Destination: &c.Destination,
			Usage:       "destination directory",
			Required:    true,
		},
		&cli.StringFlag{
			Name:        "mode",
			Aliases:     []string{"mo"},
			Destination: &c.Mode,
			Usage:       "copy or move?",
			Required:    true,
		},
		&cli.StringFlag{
			Name:        "config",
			Aliases:     []string{"c"},
			Destination: &c.ConfigPath,
			Usage:       "yaml config file path",
			DefaultText: "config.yaml",
			Required:    false,
		},
		&cli.BoolFlag{
			Name:        "no-skip",
			Destination: &c.NoSkip,
			Usage:       "no skip if file exists",
		},
		&cli.BoolFlag{
			Name:        "overwrite",
			Aliases:     []string{"o"},
			Destination: &c.OverWrite,
			Usage:       "overwrite if file exists",
		},
	},
	Action: mediaTool,
}

var extensionCommand = &cli.Command{
	Name:  "ext",
	Usage: "get all extensions for a specific dir",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:        "dir",
			Aliases:     []string{"d"},
			Destination: &c.Destination,
			Usage:       "the specific directory",
			Required:    true,
		},
	},
	Action: scanExtension,
}

func main() {
	log.SetFormatter(&log.TextFormatter{
		DisableColors:   false,
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	mediaToolApp := &cli.App{
		Name:    "media tool",
		Usage:   "你懂的",
		Version: "v0.0.1",
		Commands: []*cli.Command{
			fileCommand,
			extensionCommand,
		},
	}
	if err := mediaToolApp.Run(os.Args); err != nil {
		log.Fatal(err)
	}

}

func scanExtension(_ *cli.Context) error {
	fileList, err := walkDirectory(c.Destination)
	if err != nil {
		return err
	}
	extensionList := make([]string, 0)
	for _, file := range fileList {
		extension := getFileExtension(file, true)
		if !contains(extensionList, extension) {
			extensionList = append(extensionList, extension)
		}
	}

	for _, extension := range extensionList {
		log.Infoln(extension)
	}
	return nil
}

func contains[T comparable](elems []T, v T) bool {
	for _, e := range elems {
		if v == e {
			return true
		}
	}
	return false
}

func loadConfigFile() error {
	if c.ConfigPath == "" {
		c.ConfigPath = defaultConfigPath
	}
	log.Infof("load config file: %s\n", c.ConfigPath)
	yamlFile, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		return err
	}
	err = yaml.Unmarshal(yamlFile, &y)
	if err != nil {
		panic(err)
	}
	return nil
}

func mediaTool(_ *cli.Context) error {
	loadConfigFile()
	imageFileList, _, _, err := getMediaFileList(c.Source)
	if err != nil {
		return err
	}
	todoMap := make(map[string]string)

	for _, file := range imageFileList {
		newPath, err := processImage(file)
		if err != nil {
			continue
		}
		newPath, err = checkExist(newPath)
		if err != nil {
			continue
		}
		if newPath != "" {
			newPath = filepath.Join(c.Destination, newPath)
		}

		hit := fmt.Sprintf("Are you sure you want to %s\n%s\n-> %s?\n", c.Mode, file, newPath)
		if c.Together {
			todoMap[file] = newPath
		} else {
			if !c.Yes {
				if !askForConfirmation(hit) {
					continue
				}
			}
			err := processOneFile(file, newPath)
			if err != nil {
				continue
			}
		}
	}

	if c.Together {
		hit := "Are you sure you want to move all files?\n"
		if !c.Yes {
			if !askForConfirmation(hit) {
				return nil
			}
		}
		processFiles(todoMap)
	}

	log.Infoln("done")

	return nil
}

func askForConfirmation(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s [y/n]: ", prompt)

		response, err := reader.ReadString('\n')
		if err != nil {
			log.Fatal(err)
		}

		response = strings.ToLower(strings.TrimSpace(response))

		if response == "y" || response == "yes" {
			return true
		} else if response == "n" || response == "no" {
			return false
		}
	}
}

func processFiles(m map[string]string) {
	for s, d := range m {
		err := processOneFile(s, d)
		if err != nil {
			log.Errorf("error processing %s: %v\n", s, err)
			continue
		}
	}
}

func processOneFile(source, dest string) error {
	destinationFile, err := createDestinationFile(dest)
	if err != nil {
		return err
	}

	switch c.Mode {
	case "copy":
		err = copyFile(source, destinationFile)
		if err != nil {
			return err
		}
	case "move":
		err = moveFile(source, destinationFile)
		if err != nil {
			return err
		}
	}

	return nil
}

func checkExist(dest string) (string, error) {
	if fileExists(dest) {
		if c.OverWrite {
			return dest, nil
		}
		if !c.NoSkip {
			return "", fmt.Errorf("skip file %s", dest)
		}
		return generateNewFileName(dest), nil
	}
	return dest, nil
}

func createDestinationFile(destination string) (string, error) {
	parentDir := filepath.Dir(destination)
	if err := createParentDir(parentDir); err != nil {
		return "", err
	}
	return destination, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func createParentDir(path string) error {
	// Check if the directory already exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Create the directory and set permissions
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	return nil
}

func generateNewFileName(filename string) string {
	fileExtension := getFileExtension(filename, true)
	fileNameWithoutExtension := strings.TrimSuffix(filename, fileExtension)
	currentTime := time.Now().Format("20060102150405")
	newFileName := fileNameWithoutExtension + "_new_" + currentTime + fileExtension
	return newFileName
}

func processImage(file string) (newPath string, err error) {
	// Check if the file has any EXIF data
	newPath = readExif(file)
	if newPath != "" {
		return
	}

	// Check if the file matches the wxExport pattern
	newPath = matchWxExport(file)
	if newPath != "" {
		return
	}

	// Check if the file matches any regex pattern
	newPath = matchRegex(file)
	if newPath != "" {
		return
	}

	// If none of the conditions above are met, return an error
	return "", fmt.Errorf("failed to generate new file name for %s", file)
}

func readExif(file string) string {
	fileHandle, err := os.Open(file)
	if err != nil {
		return ""
	}

	exifData, err := exif.Decode(fileHandle)
	if err != nil {
		return ""
	}

	modelInfo, err := exifData.Get("Model")
	if err != nil {
		return ""
	}
	model := getTagString(modelInfo)

	modelAlias := y.ModelMap[model]
	if modelAlias == "" {
		modelAlias = model
	}

	timeInfo, err := exifData.Get("DateTimeOriginal")
	if err != nil {
		return ""
	}

	tm, _ := time.Parse(layout, getTagString(timeInfo))

	year := tm.Format("2006")
	month := tm.Format("01")

	fileBase := filepath.Base(file)

	return filepath.Join(modelAlias, year, month, fileBase)
}

func getTagString(tag *tiff.Tag) string {
	tagString := tag.String()
	return strings.Trim(tagString, "\"")
}

func matchWxExport(filename string) string {
	pattern := `mmexport(1\d{9})`
	regex := regexp.MustCompile(pattern)
	matches := regex.FindStringSubmatch(filename)

	if len(matches) == 0 {
		return ""
	}

	timestamp := matches[1]
	timestampInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		log.Errorf("error parsing timestamp %s: %v", timestamp, err)
		return ""
	}

	timestampTime := time.Unix(timestampInt, 0)
	year := timestampTime.Format("2006")
	month := timestampTime.Format("01")
	fileBase := filepath.Base(filename)
	newPath := filepath.Join(year, month, fileBase)

	return newPath
}

func matchRegex(file string) string {
	for pattern, layout := range regexTime {
		regex := regexp.MustCompile(pattern)
		matches := regex.FindStringSubmatch(file)
		if len(matches) > 0 {
			match := matches[0]
			t, _ := time.Parse(layout, match)
			year := t.Format("2006")
			month := t.Format("01")
			fileBase := filepath.Base(file)
			return filepath.Join(year, month, fileBase)
		}
	}
	return ""
}

func getMediaFileList(dir string) ([]string, []string, []string, error) {
	imageFiles := make([]string, 0)
	videoFiles := make([]string, 0)
	audioFiles := make([]string, 0)

	fileList, err := walkDirectory(dir)
	if err != nil {
		return nil, nil, nil, err
	}

	for _, file := range fileList {
		ext := getFileExtension(file, false)

		if picTypes[ext] {
			imageFiles = append(imageFiles, file)
		}

		if videoTypes[ext] {
			videoFiles = append(videoFiles, file)
		}

		if AudioTypes[ext] {
			audioFiles = append(audioFiles, file)
		}
	}

	return imageFiles, videoFiles, audioFiles, nil
}

func walkDirectory(dirPath string) ([]string, error) {
	var fileList []string
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil, err
	}

	err := filepath.WalkDir(dirPath, func(path string, file fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !file.IsDir() {
			fileList = append(fileList, path)
		} else {
			log.Infof("scanning dir: %s\n", path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return fileList, nil
}

func getFileExtension(path string, needDot bool) string {
	extension := filepath.Ext(path)
	if !needDot {
		extension = strings.TrimPrefix(extension, ".")
	}
	return strings.ToLower(extension)
}

func moveFile(src, dst string) error {
	return os.Rename(src, dst)
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("error opening source file: %w", err)
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("error creating destination file: %w", err)
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	if err != nil {
		return fmt.Errorf("error copying file: %w", err)
	}

	err = destination.Sync()
	if err != nil {
		return fmt.Errorf("error syncing destination file: %w", err)
	}

	return nil
}
