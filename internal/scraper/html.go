package scraper

// MarkdownFromHTML converts a local HTML document to markdown using the same
// extraction path as a scraped page.
func MarkdownFromHTML(body []byte) (string, error) {
	doc, err := parseHTML(body)
	if err != nil {
		return "", err
	}
	return htmlToMarkdown(filteredContent(doc, nil)), nil
}
