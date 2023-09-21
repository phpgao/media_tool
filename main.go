package main

import (
	"bufio"
	"errors"
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
)

const layout = "2006:01:02 15:04:05"

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

var modelAliasMap = map[string]string{
	"2304FPN6DC": "Xiaomi13Ultra",
	"22021211RC": "RedmiK40S",
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
}

var c = Config{}

func main() {
	mediaToolApp := &cli.App{
		Name:    "media tool",
		Usage:   "一键整理媒体文件",
		Version: "v0.0.1",
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
			&cli.BoolFlag{
				Name:        "no-skip",
				Destination: &c.NoSkip,
				Usage:       "don't skip file",
			},
			&cli.BoolFlag{
				Name:        "overwrite",
				Aliases:     []string{"o"},
				Destination: &c.OverWrite,
				Usage:       "overwrite if file exists",
			},
			&cli.BoolFlag{
				Name:        "yes",
				Aliases:     []string{"y"},
				Destination: &c.Yes,
				Usage:       "yes to all",
			},
			&cli.BoolFlag{
				Name:        "together",
				Aliases:     []string{"t"},
				Destination: &c.Together,
				Usage:       "process all files at once",
			},
		},
		Action: mediaTool,
	}

	if err := mediaToolApp.Run(os.Args); err != nil {
		log.Fatal(err)
	}

}

func mediaTool(_ *cli.Context) (err error) {
	// Get a list of media files from the source directory
	imageFileList, _, _, err := walk(c.Source)
	if err != nil {
		return err
	}
	todoMap := make(map[string]string)

	// Process each image file
	for _, file := range imageFileList {
		newPath, err := processImage(file)
		if err != nil {
			log.Errorf("error generating new file %s: %v", file, err)
			continue
		}
		newPath, err = checkExist(newPath)
		if err != nil {
			log.Errorf("error checking exist %s: %v", newPath, err)
			continue
		}
		hit := fmt.Sprintf("Are you sure you want to move %s to %s?\n", file, newPath)
		if c.Together {
			todoMap[file] = filepath.Join(c.Destination, newPath)
		} else {
			if !c.Yes {
				if !askForConfirmation(hit) {
					continue
				}
			}
			err := processOneFile(file, newPath)
			if err != nil {
				log.Errorf("error processing %s: %v\n", file, err)
				continue
			}
		}

	}
	if c.Together {
		hit := fmt.Sprintf("Are you sure you want to move all files?\n")
		if !c.Yes {
			if !askForConfirmation(hit) {
				return nil
			}
		}
		processFiles(todoMap)
	}

	log.Infof("done\n")

	return nil
}

func askForConfirmation(s string) bool {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("%s [y/n]: ", s)

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
	d, err := createDestinationFile(dest)
	if err != nil {
		return err
	}
	switch c.Mode {
	case "copy":
		log.Printf("%s is being copied to %s", source, d)
		err = copyFile(source, d)
		if err != nil {
			return err
		}
	case "move":
		log.Printf("%s is being moved to %s", source, d)
		err = moveFile(source, d)
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

func createDestinationFile(dst string) (string, error) {
	parentDir := filepath.Dir(dst)
	if err := createParentDir(parentDir); err != nil {
		return "", err
	}

	return dst, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func createParentDir(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	return nil
}
func generateNewFileName(filename string) string {
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	return name + "_new_" + time.Now().Format("20060102150405") + ext
}

func processImage(file string) (newPath string, err error) {
	newPath = readExif(file)
	if newPath != "" {
		return
	}

	newPath = matchWxExport(file)
	if newPath != "" {
		return
	}

	newPath = matchRegex(file)
	if newPath != "" {
		return
	}

	return "", errors.New("failed to generate new file name")
}

func readExif(file string) string {
	fileHandle, err := os.Open(file)
	if err != nil {
		return ""
	}

	x, err := exif.Decode(fileHandle)
	if err != nil {
		return ""
	}

	modelInfo, err := x.Get("Model")
	if err != nil {
		return ""
	}
	model := getTagString(modelInfo)
	modelAlias := modelAliasMap[model]

	timeInfo, err := x.Get("DateTimeOriginal")
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

func matchWxExport(filename string) (newPath string) {
	pattern := `mmexport(1\d{9})`
	regex := regexp.MustCompile(pattern)
	matches := regex.FindStringSubmatch(filename)

	if len(matches) > 0 {
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
		newPath = filepath.Join(year, month, fileBase)
	}
	return
}

func matchRegex(file string) string {
	// Iterate over the regexTime map
	for regexPattern, timeLayout := range regexTime {
		// Compile the regex pattern
		regex := regexp.MustCompile(regexPattern)
		// Find the first match in the file name
		matches := regex.FindStringSubmatch(file)
		// If a match is found
		if len(matches) > 0 {
			match := matches[0]
			// Parse the matched string as time
			tm, _ := time.Parse(timeLayout, match)
			// Extract the year and month from the parsed time
			year := tm.Format("2006")
			month := tm.Format("01")
			// Get the base file name
			fileBase := filepath.Base(file)
			// Return the path with year, month, and file name
			return filepath.Join(year, month, fileBase)
		}
	}
	// If no match is found, return an empty string
	return ""
}

func walk(dir string) (imageFiles, videoFiles, audioFiles []string, err error) {
	log.Debugf("start scanning %s", dir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil, nil, err
	}

	err = filepath.WalkDir(dir, func(path string, file fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if file.IsDir() {
			return nil
		}

		ext := getFileExtension(file.Name())

		if isPic := picTypes[ext]; isPic {
			imageFiles = append(imageFiles, path)
		}

		if isVideo := videoTypes[ext]; isVideo {
			videoFiles = append(videoFiles, path)
		}

		if isAudio := AudioTypes[ext]; isAudio {
			audioFiles = append(audioFiles, path)
		}

		return nil
	})

	if err != nil {
		return nil, nil, nil, err
	}

	return
}

func getFileExtension(path string) string {
	extension := filepath.Ext(path)
	extension = strings.TrimPrefix(extension, ".")
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
