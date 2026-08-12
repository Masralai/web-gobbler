package scraper

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const maxStoredHTML = 512 * 1024 // 512 KiB store cap for html/raw_html

func mainContent(doc *goquery.Document) *goquery.Selection {
	for _, sel := range []string{"article", "main", "[role=main]"} {
		if n := doc.Find(sel).First(); n.Length() > 0 {
			return n
		}
	}
	return doc.Find("body").First()
}

func stripNoise(sel *goquery.Selection) {
	sel.Find("script,style,nav,footer,noscript").Remove()
}

// htmlToMarkdown converts a selection's content to simple markdown (no external deps).
func htmlToMarkdown(root *goquery.Selection) string {
	var b strings.Builder
	walkMarkdown(&b, root, false)
	return strings.TrimSpace(b.String())
}

func walkMarkdown(b *strings.Builder, sel *goquery.Selection, inPre bool) {
	sel.Contents().Each(func(_ int, s *goquery.Selection) {
		if goquery.NodeName(s) == "#text" {
			text := s.Text()
			if inPre {
				b.WriteString(text)
				return
			}
			text = collapseWS(text)
			if text != "" {
				b.WriteString(text)
			}
			return
		}

		name := goquery.NodeName(s)
		switch name {
		case "script", "style", "nav", "footer", "noscript":
			return
		case "h1", "h2", "h3", "h4", "h5", "h6":
			level := int(name[1] - '0')
			b.WriteString("\n\n")
			b.WriteString(strings.Repeat("#", level))
			b.WriteString(" ")
			b.WriteString(strings.TrimSpace(s.Text()))
			b.WriteString("\n\n")
		case "p":
			b.WriteString("\n\n")
			walkMarkdown(b, s, false)
			b.WriteString("\n\n")
		case "br":
			b.WriteString("\n")
		case "a":
			href, _ := s.Attr("href")
			text := strings.TrimSpace(s.Text())
			if href != "" && text != "" {
				b.WriteString("[")
				b.WriteString(text)
				b.WriteString("](")
				b.WriteString(href)
				b.WriteString(")")
			} else if text != "" {
				b.WriteString(text)
			}
		case "ul", "ol":
			b.WriteString("\n")
			s.ChildrenFiltered("li").Each(func(_ int, li *goquery.Selection) {
				if name == "ol" {
					b.WriteString("1. ")
				} else {
					b.WriteString("- ")
				}
				b.WriteString(strings.TrimSpace(li.Text()))
				b.WriteString("\n")
			})
			b.WriteString("\n")
		case "pre":
			b.WriteString("\n\n```\n")
			walkMarkdown(b, s, true)
			b.WriteString("\n```\n\n")
		case "code":
			if inPre {
				walkMarkdown(b, s, true)
			} else {
				b.WriteString("`")
				b.WriteString(strings.TrimSpace(s.Text()))
				b.WriteString("`")
			}
		case "blockquote":
			b.WriteString("\n\n> ")
			b.WriteString(strings.TrimSpace(s.Text()))
			b.WriteString("\n\n")
		default:
			walkMarkdown(b, s, inPre)
		}
	})
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func selectionHTML(sel *goquery.Selection) string {
	html, err := goquery.OuterHtml(sel)
	if err != nil {
		return strings.TrimSpace(sel.Text())
	}
	return html
}

func truncateHTML(s string) (string, bool) {
	if len(s) <= maxStoredHTML {
		return s, false
	}
	return s[:maxStoredHTML], true
}
