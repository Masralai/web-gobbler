package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func main() {

	//command line flags
	urlFlag := flag.String("url", "", "URL to scrape")
	extractFlag := flag.String("extract", "links", "Elements to extract (e.g., 'links', 'headlines', 'all')")

	flag.Parse()

	url := *urlFlag

	if url == "" {
		fmt.Fprintln(os.Stderr, "Error: The -url flag is required")
		flag.Usage()
		os.Exit(1)
	}

	isValidURL := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	if !isValidURL {
		fmt.Fprintf(os.Stderr, "Error: Invalid URL provided: %s\n", url)
		fmt.Fprintf(os.Stderr, "URL must start with 'http://' or 'https://'")
		flag.Usage()
		os.Exit(1)
	}

	extractValue := *extractFlag
	fmt.Printf("Extraction type set to: %s\\n", extractValue)
	fmt.Println("Attempting to fetch URL", url)

	// Error checks & GET request
	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching URL %s: %v\n", url, err)
		os.Exit(1)
	}
	defer resp.Body.Close() // Schedule the closing

	//non success code
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: Received non-200 status code %d for URL %s\n", resp.StatusCode, url)
		os.Exit(1)
	}

	fmt.Println("HTTP request successful (Status 200 OK). Parsing HTML body...")

	//Check for Parsing Errors
	doc, parseErr := goquery.NewDocumentFromReader(resp.Body)
	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "Errror parsing HTML for %s: %v\\n", url, parseErr)
		os.Exit(1)
	}

	fmt.Println("HTML parsed successfully! Ready to extract data.")

	//slices for extracts
	extractedLinks := []string{}
	extractedHeaders := []string{}

	//extract links if requested
	if extractValue == "links" || extractValue == "all" {
		fmt.Println("Extracting links...")
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

	}

	//extract headers if requested
	if extractValue == "headers" || extractValue == "all" {
		fmt.Println("Extracting Headers...")
		headerSelection := doc.Find("h1,h2,h3,h4")
		fmt.Printf("Found %d headlines (h1,h2,h3,h4) on the page.\n", headerSelection.Length())
		fmt.Println("Iterating through found headline tags...")

		//Text content extraction and trimming
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
	}

	//conditionally formatting output

	//links
	if extractValue == "links" || extractValue == "all" {
		fmt.Println("\n--- Links ---")
		if len(extractedLinks) > 0 {
			for _, link := range extractedLinks {
				fmt.Println(link)
			}
		} else {
			fmt.Println("No links found or extracted.")
		}
	}

	//headers
	if extractValue == "headers" || extractValue == "all" {
		fmt.Println("\n--- Headers ---")
		if len(extractedHeaders) > 0 {
			for _, header := range extractedHeaders {
				fmt.Println(header)
			}
		} else {
			fmt.Println("No headlines found or extracted.")
		}
	}

	if extractValue != "links" && extractValue != "headers" && extractValue != "all" {
		fmt.Fprintf(os.Stderr, "\\nWarning: Invalid value '%s' provided for -extract flag ", extractValue)
		fmt.Fprintln(os.Stderr, "Valid options are 'links', 'headlines', or 'all'. No data extracted/printed.")
	}

	fmt.Println("\nScraping process finished.")
}
