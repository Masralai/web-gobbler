# Go Web Scraper

A simple yet powerful command-line web scraper built in Go that extracts links and headers from web pages with flexible output options.

## Features

- **Flexible Extraction**: Extract links, headers, or both from any web page
- **Output Options**: Display results in console or save to a file
- **URL Validation**: Robust URL parsing and validation
- **Absolute URL Resolution**: Converts relative links to absolute URLs
- **Error Handling**: Comprehensive error checking and user-friendly messages
- **Clean Output**: Organized formatting with clear section headers

## Installation

### Prerequisites

- Go 1.16 or higher
- Internet connection for fetching dependencies

### Install Dependencies

```bash
go mod init web-scraper
go get github.com/PuerkitoBio/goquery
```

### Build the Application

```bash
go build -o scraper main.go
```

## Usage

### Basic Syntax

```bash
./scraper -url <URL> [OPTIONS]
```

### Command Line Flags

| Flag       | Required | Description                                             | Default |
| ---------- | -------- | ------------------------------------------------------- | ------- |
| `-url`     | Yes      | Target URL to scrape (must include http:// or https://) | -       |
| `-extract` | No       | Elements to extract: `links`, `headers`, or `all`       | `links` |
| `-output`  | No       | Output file path (if not specified, prints to console)  | console |

### Examples

#### Extract all links from a webpage

```bash
./scraper -url https://github.com
```

#### Extract headers only

```bash
./scraper -url https://example.com -extract headers
```

#### Extract both links and headers

```bash
./scraper -url https://news.ycombinator.com -extract all
```

#### Save results to a file

```bash
./scraper -url https://github.com -extract all -output report.txt
```

#### Run directly with Go

```bash
go run main.go -url https://github.com -extract all -output "report.txt"
```

## Output Format

The scraper organizes extracted data into clear sections:

### Links Section

```
--- Links ---
https://example.com/page1
https://example.com/page2
https://external-site.com/resource
```

### Headers Section

```
--- Headlines ---
Welcome to Our Website
Latest News
Contact Information
```

## Technical Details

### Dependencies

- **goquery**: HTML parsing and DOM manipulation
- **net/http**: HTTP client functionality
- **net/url**: URL parsing and resolution
- **flag**: Command-line argument parsing

### What Gets Extracted

#### Links (`-extract links`)

- All `<a>` tags with `href` attributes
- Relative URLs are converted to absolute URLs
- Malformed URLs are skipped with warnings

#### Headers (`-extract headers`)

- HTML header tags: `<h1>`, `<h2>`, `<h3>`, `<h4>`
- Empty headers are filtered out
- Whitespace is trimmed from header text

## Error Handling

The scraper includes comprehensive error handling for:

- Missing or invalid URLs
- Network connection issues
- Non-200 HTTP status codes
- HTML parsing errors
- File creation/writing errors
- Malformed links (with warnings)

## Example Output

```bash
$ ./scraper -url https://example.com -extract all -output results.txt

Base URL successfully parsed: https://example.com
Extraction type set to: all
Successfully opened file results.txt for writing.
Attempting to fetch URL https://example.com
HTTP request successful (Status 200 OK). Parsing HTML body...
HTML parsed successfully! Ready to extract data.
Extracting links...
Found 15 link(s) on the page.
Successfully stored 15 links.
Extracting Headers...
Found 8 headlines (h1,h2,h3,h4) on the page.
Successfully stored 6 non-empty headline(s).

Scraping process finished.
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Disclaimer

Please use this tool responsibly and in accordance with websites' robots.txt files and terms of service. Always respect rate limits and avoid overwhelming servers with requests.
