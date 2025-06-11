package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func main() {
	// Arg Count Check
	if len(os.Args) <= 1 {
		fmt.Println("Usage: go run main.go <URL>")
		os.Exit(1)
	}

	// URL Validation
	url := os.Args[1]

	isValidURL := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	if !isValidURL {
		fmt.Fprintf(os.Stderr, "Error: Invalid URL provided: %s\n", url)
		fmt.Fprintf(os.Stderr, "URL must start with 'http://' or 'https://'")
		os.Exit(1)
	}
	fmt.Println("Validated URL provided: ", url)

	resp, err := http.Get(url)

	// Error checks
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching URL %s: %v\n", url, err)
		os.Exit(1)
	}
	defer resp.Body.Close() // Schedule the closing

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: Received non-200 status code %d for URL %s\n", resp.StatusCode, url)
		os.Exit(1)
	}

	fmt.Println("HTTP request successful (no network error). Status check & body processing needed.")

	//Check for Parsing Errors
	doc, parseErr := goquery.NewDocumentFromReader(resp.Body)
	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "Errror parsing HTML for %s: %v\\n", url, parseErr)
	}

	fmt.Println("HTML parsed successfully! Ready to extract data.")

	extractedLinks := []string{}

	// SELECT ALL ANCHOR ELEMENTS
	linkSelection := doc.Find("a")
	fmt.Printf("Found %d link(s) on the page.\\n", linkSelection.Length())

	fmt.Println("Iterating through links...")
	linkSelection.Each(func(i int, element *goquery.Selection) {
		hrefValue, hrefExists := element.Attr("href")
		if hrefExists {
			extractedLinks = append(extractedLinks, hrefValue)
		}
	})
	fmt.Println("Finished iterating through <a> tags.")
	//Link Extraction Complete
	fmt.Printf("Successfully stored %d links.\n ---\n", len(extractedLinks))

	//Finding Headers
	headerSelection := doc.Find("h1,h2,h3,h4")

	fmt.Printf("Found %d headlines (h1,h2,h3,h4) on the page.\n", headerSelection.Length())

	fmt.Println("Iterating through found headline tags...")

	//Text content extraction and trimming
	extractedHeaders := []string{}
	headerSelection.Each(func(i int, element *goquery.Selection) {
		headerText := element.Text()
		headerText = strings.TrimSpace(headerText)

		if headerText != "" {
			fmt.Printf("Header #%d cleaned text : [%s]\n ", i, headerText)
			extractedHeaders = append(extractedHeaders, headerText)
			fmt.Println(extractedHeaders)
		}
	})
	fmt.Println("Finished iterating through headline tags.")
	fmt.Printf("Successfully stored %d non-empty headline(s).\\n", len(extractedHeaders))

	//formatted output section
	fmt.Println("\n--- Links ---")

	//LOOP AND PRINT EACH EXTRACTED LINK
	for _, link := range extractedLinks {
		fmt.Println(link)
	}

	fmt.Println("\n--- Headers ---")

	for _, header := range extractedHeaders {
		fmt.Println(header)
	}

}
