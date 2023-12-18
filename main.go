package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"

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
	acceptAuthors        []string
	interestingTopics    []string
	interestingDomains   []string
	mutedWords           []string
	territory            string
	checkInterval        time.Duration
	targetURL            string
	nostrNotifierPrivKey string
	nostrRecipientPubKey string
	nostrRelays          []string
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
	mutedWordsPtr := flag.String("mute", "", "Comma-separated list of muted words (applied to authors, topics, domains)")
	territoryPtr := flag.String("territory", "", "Territory, default is home (all)")
	nostrNotifierPrivKeyPtr := flag.String("nostr-from", "", "Nostr private hex key of the notifier")
	nostrRecipientPubKeyPtr := flag.String("nostr-to", "", "Nostr public hex key of the recipient (you!)")
	nostrRelaysPtr := flag.String("nostr-relays", "wss://nostr-pub.wellorder.net,wss://nos.lol,wss://relay.damus.io", "Nostr relays")
	checkIntervalPtr := flag.Int("interval", 5, "Interval check in minutes, default is 5")

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
	if *mutedWordsPtr != "" {
		mutedWords = strings.Split(*mutedWordsPtr, ",")
	}
	if *territoryPtr != "" {
		territory = *territoryPtr
	}

	if territory != "" {
		targetURL = "https://stacker.news/~" + territory + "/recent"
	} else {
		targetURL = "https://stacker.news/recent"
	}

	if *nostrNotifierPrivKeyPtr != "" {
		nostrNotifierPrivKey = *nostrNotifierPrivKeyPtr
	}

	if *nostrRecipientPubKeyPtr != "" {
		nostrRecipientPubKey = *nostrRecipientPubKeyPtr
	}

	if *nostrRelaysPtr != "" {
		nostrRelays = strings.Split(*nostrRelaysPtr, ",")
	}

	if (nostrNotifierPrivKey+nostrRecipientPubKey) != "" && (nostrNotifierPrivKey == "" || nostrRecipientPubKey == "") {
		fmt.Println(customMagenta + "You must provide both -nostr-from and -nostr-to" + reset)
		os.Exit(0)
	}

	checkInterval = time.Duration(*checkIntervalPtr) * time.Minute

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
			(isIncluded(author, acceptAuthors, true) ||
				isIncluded(title, interestingTopics, false) ||
				isIncluded(domain, interestingDomains, false)) &&
			!isIncluded(author, mutedWords, true) &&
			!isIncluded(title, mutedWords, false) &&
			!isIncluded(domain, mutedWords, false) {
			localTime := parsedDate.Local().Format("2006-01-02 15:04")
			fmt.Println(customMagenta + author + reset + " - " + customGray + localTime + reset)
			fmt.Println(customBlu + title + reset)
			nostrNote := author + " - " + localTime + "\n" + title + "\n"
			if domain != "" {
				fmt.Println(domain)
				nostrNote = nostrNote + domain + "\n"
			}
			fmt.Println(customYellow + "https://stacker.news" + url + reset + "\n")
			nostrNote = nostrNote + "\nhttps://stacker.news" + url

			if nostrNotifierPrivKey != "" {
				nostrNotify(nostrNote)
			} else {
				playBeep()
			}
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

func isIncluded(text string, list []string, fullMatch bool) bool {
	if len(list) == 0 {
		return false
	}
	lowercaseText := strings.ToLower(text)
	for _, el := range list {
		if fullMatch && strings.ToLower(el) == lowercaseText {
			return true
		} else if strings.Contains(lowercaseText, strings.ToLower(el)) {
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

func nostrNotify(payload string) {
	sk := nostrNotifierPrivKey
	pub, _ := nostr.GetPublicKey(sk)

	ev := nostr.Event{
		PubKey:    pub,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindEncryptedDirectMessage,
		Tags:      nostr.Tags{nostr.Tag{"p", nostrRecipientPubKey}},
	}

	// calling Sign sets the event ID field and the event Sig field
	ss, err := nip04.ComputeSharedSecret(nostrRecipientPubKey, sk)
	if err != nil {
		fmt.Println(err)
		os.Exit(0)
	}
	ev.Content, err = nip04.Encrypt(payload, ss)
	if err != nil {
		fmt.Println(err)
		os.Exit(0)
	}
	ev.Sign(sk)

	// publish the event to two relays
	ctx := context.Background()
	for _, url := range nostrRelays {
		relay, err := nostr.RelayConnect(ctx, url)
		if err != nil {
			fmt.Println(err)
			continue
		}
		if err := relay.Publish(ctx, ev); err != nil {
			fmt.Println(err)
			continue
		}
	}
}
