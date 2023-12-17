package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/faiface/beep"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/wav"
)

//go:embed alert.wav
var alertSound []byte

const intitialCheck = 300 * time.Minute

var lastChecked time.Time

var (
	acceptAuthors      []string
	interestingTopics  []string
	interestingDomains []string
	territory          string
	checkInterval      time.Duration
	targetURL          string
)

const (
	customGray    = "\x1b[37m"
	customBlu     = "\x1b[36m"
	customMagenta = "\x1b[38;5;198m"
	customYellow  = "\x1b[38;5;220m"
	reset         = "\x1b[0m" // Reset flag
)

func main() {
	fmt.Println("")

	authorsPtr := flag.String("authors", "", "Comma-separated list of accepted authors")
	topicsPtr := flag.String("topics", "", "Comma-separated list of interesting topics")
	domainsPtr := flag.String("domains", "", "Comma-separated list of interesting domains")
	territoryPtr := flag.String("territory", "", "Territory, default is home (all)")
	checkIntervalValue := flag.Int("interval", 5, "Interval check in minutes, default is 5")

	flag.Parse()

	if (*authorsPtr == "") && (*topicsPtr == "") && (*domainsPtr == "") {
		fmt.Println(customMagenta + "You need to give me some filters!" + reset + "\n")
		flag.Usage()
		os.Exit(0)
	}

	if *authorsPtr != "" {
		acceptAuthors = strings.Split(*authorsPtr, ",")
	}
	if *topicsPtr != "" {
		interestingTopics = strings.Split(*topicsPtr, ",")
	}
	if *domainsPtr != "" {
		interestingDomains = strings.Split(*domainsPtr, ",")
	}
	if *territoryPtr != "" {
		territory = *territoryPtr
	}

	if territory != "" {
		targetURL = "https://stacker.news/~" + territory + "/recent"
	} else {
		targetURL = "https://stacker.news/recent"
	}

	checkInterval = time.Duration(*checkIntervalValue) * time.Minute

	playBeep()
	// Run initially and start the ticker for periodic scraping
	checkForNewItems(intitialCheck)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			checkForNewItems(checkInterval)
		}
	}
}

func checkForNewItems(interval time.Duration) {
	resp, err := http.Get(targetURL)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Error: status code %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	doc.Find(".item_hunk__DFX1z").Each(func(i int, s *goquery.Selection) {
		htmlContent, _ := goquery.OuterHtml(s)
		author := extractAuthor(htmlContent)
		date := extractDate(htmlContent)
		title, url := extractTitleAndURL(htmlContent)
		domain := extractDomain(htmlContent)
		parsedDate, err := time.Parse(time.RFC3339, date)
		if err != nil {
			fmt.Println("Error parsing date:", err)
			return
		}
		if time.Since(parsedDate) < interval &&
			(isAuthorAccepted(author) ||
				containsInterestingTopic(title) ||
				containsInterestingDomains(domain)) {
			localTime := parsedDate.Local()
			fmt.Println(customMagenta + author + reset + " - " + customGray + localTime.Format("2006-01-02 15:04") + reset)
			fmt.Println(customBlu + title + reset)
			if domain != "" {
				fmt.Println(domain)
			}
			fmt.Println(customYellow + "https://stacker.news" + url + reset + "\n")
			playBeep()
		}
	})

	lastChecked = time.Now()
}

func extractAuthor(htmlString string) string {
	re := regexp.MustCompile(`@<!-- -->(\w+)<span>`)
	matches := re.FindStringSubmatch(htmlString)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func isAuthorAccepted(author string) bool {
	if len(acceptAuthors) == 0 {
		return false
	}
	for _, accepted := range acceptAuthors {
		if author == accepted {
			return true
		}
	}
	return false
}

func extractDate(htmlString string) string {
	re := regexp.MustCompile(`title="(2023[^"]*)"`)
	matches := re.FindStringSubmatch(htmlString)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func extractTitleAndURL(htmlString string) (string, string) {
	re := regexp.MustCompile(`<a[^>]*\bclass="item_title[^"]*"[^>]*\bhref="([^"]+)"[^>]*>([^<]+)</a>`)
	matches := re.FindStringSubmatch(htmlString)
	if len(matches) >= 3 {
		return matches[2], matches[1]
	}
	return "", ""
}

func extractDomain(htmlString string) string {
	re := regexp.MustCompile(`<a[^>]*\bclass="item_link[^"]*"[^>]*\bhref="([^"]+)"[^>]*>[^<]+</a>`)
	matches := re.FindStringSubmatch(htmlString)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func containsInterestingTopic(text string) bool {
	if len(interestingTopics) == 0 {
		return false
	}
	lowercaseText := strings.ToLower(text)
	for _, topic := range interestingTopics {
		if strings.Contains(lowercaseText, topic) {
			return true
		}
	}
	return false
}

func containsInterestingDomains(url string) bool {
	if len(interestingDomains) == 0 {
		return false
	}
	lowercaseText := strings.ToLower(url)
	for _, topic := range interestingDomains {
		if strings.Contains(lowercaseText, topic) {
			return true
		}
	}
	return false
}

func playBeep() {
	f, err := os.CreateTemp("", "temp_*.wav")
	if err != nil {
		fmt.Println("Error creating temporary WAV file:", err)
		return
	}
	defer os.Remove(f.Name())

	_, err = f.Write(alertSound)
	if err != nil {
		fmt.Println("Error writing temporary WAV file:", err)
		return
	}

	file, err := os.Open(f.Name())
	if err != nil {
		fmt.Println("Error opening temporary WAV file:", err)
		return
	}
	defer file.Close()

	streamer, format, err := wav.Decode(file)
	if err != nil {
		fmt.Println("Error decoding WAV file:", err)
		return
	}
	defer streamer.Close()

	err = speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
	if err != nil {
		fmt.Println("Error initializing speaker:", err)
		return
	}

	done := make(chan struct{})
	speaker.Play(beep.Seq(streamer, beep.Callback(func() {
		close(done)
	})))

	<-done
}
