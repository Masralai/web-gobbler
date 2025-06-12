package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func main() {

	//command line flags
	urlFlag := flag.String("url", "", "URL to scrape")
	extractFlag := flag.String("extract", "links", "Elements to extract (e.g., 'links', 'headlines', 'all')")
	outputFlag := flag.String("output", "", "output file path(optional)")

	flag.Parse()
	target_url := *urlFlag

	//validate the url flag
	if target_url == "" {
		fmt.Fprintln(os.Stderr, "Error: The -url flag is required")
		flag.Usage()
		os.Exit(1)
	}

	//url prefix validation
	isValidURL := strings.HasPrefix(target_url, "http://") || strings.HasPrefix(target_url, "https://")
	if !isValidURL {
		fmt.Fprintf(os.Stderr, "Error: Invalid URL provided: %s\n", target_url)
		fmt.Fprintf(os.Stderr, "URL must start with 'http://' or 'https://'")
		//flag.Usage()
		os.Exit(1)
	}

	//parse the base url via the flag
	baseURL, err := url.Parse(target_url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to parse base URL '%s': %v\n", target_url, err)
		os.Exit(1)
	}
	fmt.Printf("Base URL successfully parsed: %s\n", baseURL.String()) // Confirmation message

	//value of extract flag
	extractValue := *extractFlag
	fmt.Printf("Extraction type set to: %s\\n", extractValue)

	var file *os.File
	var fileErr error

	//value of output flag
	outputFilePath := *outputFlag
	if outputFilePath != "" {
		fmt.Printf("Attempting to create/open output file: %s\n", outputFilePath)
		file, fileErr := os.Create(outputFilePath)

		if fileErr != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not create output file '%s': %v\n", outputFilePath, fileErr)
			os.Exit(1)
		}
		fmt.Printf("Successfully opened file %s for writing.\n", outputFilePath)
		defer file.Close()
	} else {
		fmt.Println("Output will be printed to the console.")
	}
	fmt.Println("Attempting to fetch URL", target_url)

	// Error checks & GET request
	resp, httpErr := http.Get(target_url)
	if httpErr != nil {
		fmt.Fprintf(os.Stderr, "Error fetching URL %s: %v\n", target_url, httpErr)
		os.Exit(1)
	}
	defer resp.Body.Close() // Schedule the closing

	//non success code
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: Received non-200 status code %d for URL %s\n", resp.StatusCode, target_url)
		os.Exit(1)
	}

	fmt.Println("HTTP request successful (Status 200 OK). Parsing HTML body...")

	// Parsing and Error checks
	doc, parseErr := goquery.NewDocumentFromReader(resp.Body)
	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "Errror parsing HTML for %s: %v\\n", target_url, parseErr)
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

		linkSelection.Each(func(index int, element *goquery.Selection) {
			hrefValue, hrefExists := element.Attr("href")
			if hrefExists {
				linkURL, parseLinkErr := url.Parse(hrefValue)
				if parseLinkErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: Skipping malformed link #%d: '%s' - Error: %v\n", index+1, hrefValue, parseLinkErr)
					return
				}
				absoluteURL := (baseURL.ResolveReference(linkURL)).String()
				extractedLinks = append(extractedLinks, absoluteURL)
				fmt.Printf("      Raw href: %s -> Resolved: %s\n", hrefValue, absoluteURL)
			}
		})
		fmt.Println("Finished iterating through <a> tags.")
		fmt.Printf("Successfully stored %d links.\n ---\n", len(extractedLinks))
	}

	//extract headers if requested
	if extractValue == "headers" || extractValue == "all" {
		fmt.Println("Extracting Headers...")
		headerSelection := doc.Find("h1,h2,h3,h4")
		fmt.Printf("Found %d headlines (h1,h2,h3,h4) on the page.\n", headerSelection.Length())
		fmt.Println("Iterating through found headline tags...")

		headerSelection.Each(func(index int, element *goquery.Selection) {
			headerText := element.Text()
			headerText = strings.TrimSpace(headerText) //trimming white spaces
			if headerText != "" {
				extractedHeaders = append(extractedHeaders, headerText)
				fmt.Println(extractedHeaders)
			}
		})
		fmt.Println("Finished iterating through headline tags.")
		fmt.Printf("Successfully stored %d non-empty headline(s).\\n", len(extractedHeaders))
	}

	//conditionally formatting output
	var outputWriter *os.File = os.Stdout
	if file != nil {
		outputWriter = file
	}

	//links
	if extractValue == "links" || extractValue == "all" {
		fmt.Fprintln(outputWriter, "\\n--- Links ---")
		if len(extractedLinks) > 0 {
			for _, link := range extractedLinks {
				fmt.Fprintln(outputWriter, link)
			}
		} else {
			fmt.Fprintln(outputWriter, "No links found or extracted.")
		}
	}

	//headers
	if extractValue == "headers" || extractValue == "all" {
		fmt.Fprintln(outputWriter, "\\n--- Headlines ---")
		if len(extractedHeaders) > 0 {
			for _, header := range extractedHeaders {
				fmt.Fprintln(outputWriter, header)
			}
		} else {
			fmt.Fprintln(outputWriter, "No headlines found or extracted.")
		}
	}

	if extractValue != "links" && extractValue != "headers" && extractValue != "all" {
		fmt.Fprintf(os.Stderr, "\\nWarning: Invalid value '%s' provided for -extract flag ", extractValue)
		fmt.Fprintln(os.Stderr, "Valid options are 'links', 'headlines', or 'all'. No data extracted/printed.")
	}

	fmt.Println("\nScraping process finished.")
}
