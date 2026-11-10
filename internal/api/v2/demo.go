package v2

import (
	"html/template"

	"github.com/gin-gonic/gin"
)

func (h *Handler) HandleDemo(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = template.Must(template.New("demo").Parse(demoHTML)).Execute(c.Writer, map[string]any{
		"apiKey": h.Config.APIKey,
		"demo":   h.Config.DemoMode,
	})
}

const demoHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Web Gobbler — Firecrawl-compatible scraper</title>
<style>
body{font:15px/1.5 system-ui,sans-serif;max-width:900px;margin:40px auto;padding:0 20px;color:#111}
h1{font-size:24px}form{display:flex;gap:8px;margin:20px 0}
input{flex:1;padding:10px;border:1px solid #bbb;border-radius:6px}
button{padding:10px 16px;border:0;border-radius:6px;background:#111;color:#fff;cursor:pointer}
pre{background:#f4f4f4;padding:16px;border-radius:6px;overflow:auto;max-height:520px;white-space:pre-wrap}
small{color:#666}.row{display:flex;gap:8px;flex-wrap:wrap}
</style>
</head>
<body>
<h1>Web Gobbler</h1>
<p>Self-hosted web scraper. Paste a URL and get LLM-ready markdown.</p>
{{if .demo}}<p><small>Demo mode: extract disabled, crawl capped.</small></p>{{end}}
<form id="f">
  <input id="url" value="https://example.com" placeholder="https://example.com" required>
  <button>Scrape</button>
</form>
<div class="row">
  <button type="button" data-url="https://example.com">example.com</button>
</div>
<pre id="out">Ready.</pre>
<script>
const key = {{printf "%q" .apiKey}};
document.querySelectorAll('[data-url]').forEach(b => b.onclick = () => { document.getElementById('url').value = b.dataset.url; });
document.getElementById('f').onsubmit = async (e) => {
  e.preventDefault();
  const out = document.getElementById('out');
  out.textContent = 'Scraping...';
  const headers = {'Content-Type':'application/json'};
  if (key) headers.Authorization = 'Bearer ' + key;
  try {
    const res = await fetch('/v2/scrape', {method:'POST',headers,body:JSON.stringify({url:document.getElementById('url').value,formats:['markdown','links']})});
    const body = await res.json();
    out.textContent = JSON.stringify(body, null, 2);
  } catch (err) { out.textContent = String(err); }
};
</script>
</body>
</html>`
