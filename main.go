package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	apiEndpoint = "https://api.dhbw.app/rapla/lectures/KA-TINF25B6/events"
	bgWhite     = "#[bg=lightgrey,fg=color237]"
	bgReset     = "#[bg=color238,fg=lightgrey]"
	fullBlock   = "█"
	emptyBlock  = " "
	suffix      = "#[nobold,fg=colour242] |"
)

var (
	cacheFile           = os.ExpandEnv("$HOME/dev/go/loslassen.timer/cache.json")
	blocks              = [...]string{" ", "▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
	courseAbbreviations = map[string]string{
		"Algorithm and complexity":            "ALGO",
		"Analysis":                            "ANA",
		"BWL":                                 "BWL",
		"Digitaltechnik":                      "DIGI",
		"Intercultural Communication Group A": "ICC",
		"Intercultural Communication Group B": "ICC",
		"Lineare Algebra":                     "LA",
		"Programmieren":                       "PROG",
		"Projektmanagement":                   "PM",
		"TheoInfo1: Grundlagen und Logik":     "THEO",
		"Web Engineering":                     "WEB",
	}
)

func main() {
	// Define the flag: name, default value, and description
	barSize := flag.Int("size", 10, "Length of the progress bar")
	cacheTTL := flag.Int("ttl", 15, "TTL of the cache in minutes")
	flag.Parse() // Always call this to parse the arguments

	if *barSize < 5 {
		fmt.Println("Fatal: Size must be at least 5: ", *barSize, " < 5")
		os.Exit(1)
	}

	lectures, err := retrieveLectures(*cacheTTL)
	if err != nil {
		fmt.Println(err)
		return
	}

	sort.Sort(byEndTime(lectures))

	nextEndingLecture := getNextEndingLecture(lectures)
	// Pass the value (dereferenced) to your function
	printTimer(nextEndingLecture, *barSize)
}

func getNextEndingLecture(sortedLecture []Lecture) Lecture {
	now := time.Now()
	low, high := 0, len(sortedLecture)-1
	ansIdx := -1

	for low <= high {
		mid := low + (high-low)/2

		if now.Before(sortedLecture[mid].EndTime) {
			ansIdx = mid
			high = mid - 1
		} else {
			low = mid + 1
		}
	}

	if ansIdx == -1 {
		return Lecture{} // All lectures have ended
	}
	return sortedLecture[ansIdx]
}

func printTimer(lecture Lecture, size int) {
	now := time.Now()
	if now.Before(lecture.StartTime) {
		minutesUntilStart := int(lecture.StartTime.Sub(now).Minutes()) + 1 // +1 to "round up"
		if minutesUntilStart <= 30 {
			lectureName, ok := courseAbbreviations[lecture.Name]
			if !ok {
				lectureName = lecture.Name
			}
			fmt.Printf("%s in %dmin%s", lectureName, minutesUntilStart, suffix)
		}
	} else {
		totalTime := lecture.EndTime.Sub(lecture.StartTime)
		passedTime := now.Sub(lecture.StartTime)
		passedPercent := passedTime.Seconds() / totalTime.Seconds()

		printBar(passedPercent*100, size, lecture)
	}
}

func printBar(percent float64, size int, lecture Lecture) {
	var output strings.Builder

	loc, _ := time.LoadLocation("Europe/Berlin")
	lectureEndTime := lecture.EndTime.In(loc).Format("15:04")

	output.WriteString("[#[bg=color238]")

	fullBlocks := int(percent * float64(size) / 100.0)
	exactFullBlocks := percent / 100.0 * float64(size)
	remainder := exactFullBlocks - float64(fullBlocks)

	if size-fullBlocks > len(lectureEndTime) {
		// Write Time at the end of the empty bar
		for range fullBlocks {
			output.WriteString(fullBlock)
		}

		if fullBlocks < size-len(lectureEndTime) {
			index := int(remainder * float64(len(blocks)))
			output.WriteString(blocks[index])

			// Fill remaining empty space
			for i := fullBlocks + 1; i < size-len(lectureEndTime); i++ {
				output.WriteString(emptyBlock)
			}
		}

		output.WriteString(lectureEndTime)
	} else {
		// Write Time at the end of the full bar

		for range fullBlocks - len(lectureEndTime) {
			output.WriteString(fullBlock)
		}

		output.WriteString(bgWhite)
		output.WriteString(lectureEndTime)
		output.WriteString(bgReset)

		if fullBlocks < size {
			index := int(remainder * float64(len(blocks)))
			output.WriteString(blocks[index])

			// Fill remaining empty space
			for i := fullBlocks + 1; i < size; i++ {
				output.WriteString(emptyBlock)
			}
		}
	}

	output.WriteString("#[bg=default]]#[nobold]")
	fmt.Fprintf(&output, " %.0f%%", percent)
	output.WriteString(suffix)
	fmt.Println(output.String())
}

func retrieveLectures(cacheTTL int) ([]Lecture, error) {
	cachedLectures, err := readCache()
	if err != nil || isCacheOutdated(cachedLectures, cacheTTL) {
		lectures, err := fetchLectures()
		if err == nil {
			writeCache(lectures)
		}
		return lectures, err
	}

	return cachedLectures.Lectures, nil
}

func readCache() (CachedLectures, error) {
	var data CachedLectures

	fileData, err := os.ReadFile(cacheFile)
	if err != nil {
		return data, err
	}

	err = json.Unmarshal(fileData, &data)
	return data, err
}

func isCacheOutdated(cache CachedLectures, cacheTTL int) bool {
	return time.Since(cache.LastRetrieved).Minutes() > float64(cacheTTL)
}

func writeCache(lectures []Lecture) error {
	data := CachedLectures{time.Now(), lectures}

	fileData, err := json.MarshalIndent(data, "", "  ") // Indent makes it readable
	if err != nil {
		return err
	}

	return os.WriteFile(cacheFile, fileData, 0o644)
}

func fetchLectures() ([]Lecture, error) {
	resp, err := http.Get(apiEndpoint)
	if err != nil {
		return nil, &fetchError{"Failed to fetch from API endpoint."}
	}
	defer resp.Body.Close()

	var rawItems []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&rawItems); err != nil {
		return nil, &fetchError{"Failed to decode JSON array."}
	}

	var lectures []Lecture
	for _, item := range rawItems {
		// Helper to extract only the type
		var typeCheck struct {
			EntityType string `json:"entityType"`
		}

		if err := json.Unmarshal(item, &typeCheck); err == nil && typeCheck.EntityType == "LECTURE" {
			var l Lecture
			if err := json.Unmarshal(item, &l); err == nil {
				lectures = append(lectures, l)
			}
		}
	}

	return lectures, nil
}
