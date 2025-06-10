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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK{
		fmt.Fprintf(os.Stderr,"Error: Received non-200 status code %d for URL %s\n",resp.StatusCode,url)
		os.Exit(1)
	}

	fmt.Println("HTTP request successful (no network error). Status check & body processing needed.")
}
