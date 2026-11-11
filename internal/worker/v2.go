package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	apiv2 "github.com/Masralai/web-gobbler/internal/api/v2"
	"github.com/Masralai/web-gobbler/internal/crawl"
	"github.com/Masralai/web-gobbler/internal/extract"
	"github.com/Masralai/web-gobbler/internal/metrics"
	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/google/uuid"
)

type V2Worker struct {
	Store            *store.Store
	Queue            *queue.Queue
	Extractor        extract.Extractor
	LLM              bool
	WebhookSecret    string
	RetentionMinutes int
}

func (w *V2Worker) Run(ctx context.Context, capability string, concurrency int) {
	stream := queue.StreamDefault
	if capability == "browser" {
		stream = queue.StreamBrowser
	}
	group := "workers"
	if err := w.Queue.EnsureGroup(ctx, stream, group); err != nil {
		slog.Error("create consumer group", "stream", stream, "error", err)
		return
	}
	if concurrency < 1 {
		concurrency = 1
	}
	for i := 0; i < concurrency; i++ {
		go w.loop(ctx, stream, group, fmt.Sprintf("%s-%d-%s", capability, i, uuid.NewString()[:8]))
	}
	go w.reaper(ctx)
}

func (w *V2Worker) loop(ctx context.Context, stream, group, consumer string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgs, err := w.Queue.ReadGroup(ctx, stream, group, consumer, 1, 5*time.Second)
		if err != nil {
			slog.Error("read stream", "stream", stream, "error", err)
			time.Sleep(time.Second)
			continue
		}
		if len(msgs) == 0 {
			claimed, err := w.Queue.AutoClaim(ctx, stream, group, consumer, 60*time.Second, 1)
			if err == nil {
				msgs = claimed
			}
		}
		for _, msg := range msgs {
			if err := w.process(ctx, msg); err != nil {
				slog.Error("process message", "job_id", msg.Payload.JobID, "error", err)
				continue
			}
			_ = w.Queue.Ack(ctx, stream, group, msg.ID)
		}
	}
}

func (w *V2Worker) process(ctx context.Context, msg queue.StreamMessage) error {
	id, err := uuid.Parse(msg.Payload.JobID)
	if err != nil {
		return w.Queue.Ack(ctx, queue.StreamDefault, "workers", msg.ID)
	}
	claimed, err := w.Store.SetV2Processing(ctx, id)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	job, err := w.Store.GetV2Job(ctx, id)
	if err != nil {
		return err
	}
	start := time.Now()
	switch job.Kind {
	case store.KindPage:
		err = w.processPage(ctx, job)
	case store.KindCrawl:
		err = w.processCrawl(ctx, job)
	case store.KindExtract:
		err = w.processExtract(ctx, job)
	case store.KindScrape:
		err = w.processScrapeJob(ctx, job)
	default:
		err = fmt.Errorf("unknown job kind %q", job.Kind)
	}
	if err != nil {
		msgText := err.Error()
		_ = w.Store.UpdateV2Job(ctx, id, "failed", nil, &msgText, nil, 0, int64Ptr(time.Since(start).Milliseconds()))
		if job.Kind == store.KindExtract {
			w.deliver(ctx, job, "extract.failed", map[string]any{"success": false, "error": msgText})
		}
		return err
	}
	return nil
}

func (w *V2Worker) processScrapeJob(ctx context.Context, job *store.V2Job) error {
	var req apiv2.ScrapeRequest
	if err := json.Unmarshal(job.Request, &req); err != nil {
		return err
	}
	doc, err := w.scrape(ctx, req)
	if err != nil {
		return err
	}
	doc.Metadata.ScrapeID = job.ID.String()
	raw, _ := json.Marshal(doc)
	return w.Store.UpdateV2Job(ctx, job.ID, "completed", raw, nil, &doc.Metadata.StatusCode, 0, int64Ptr(0))
}

func (w *V2Worker) processPage(ctx context.Context, job *store.V2Job) error {
	if job.ParentID != nil {
		if p, err := w.Store.GetV2Job(ctx, *job.ParentID); err == nil {
			if p.Status == "cancelled" {
				return nil
			}
			if p.Kind == store.KindBatch && p.Status == "queued" {
				if claimed, _ := w.Store.SetV2Processing(ctx, p.ID); claimed {
					w.deliver(ctx, p, "batch_scrape.started", map[string]any{"success": true})
					_ = w.Store.ClearV2LeaseKeepProcessing(ctx, p.ID)
				}
			}
		}
	}
	var req apiv2.BatchRequest
	if len(job.Request) > 0 {
		_ = json.Unmarshal(job.Request, &req)
	}
	opts := apiv2.DefaultScrapeOptions()
	if req.ScrapeOptions != nil {
		opts = *req.ScrapeOptions
	}
	doc, err := w.scrape(ctx, apiv2.ScrapeRequest{URL: job.URL, ScrapeOptions: opts})
	if err != nil {
		if job.ParentID != nil {
			_ = w.maybeCompleteBatch(ctx, *job.ParentID)
		}
		return err
	}
	doc.Metadata.ScrapeID = job.ID.String()
	raw, _ := json.Marshal(doc)
	if err := w.Store.UpdateV2Job(ctx, job.ID, "completed", raw, nil, &doc.Metadata.StatusCode, 0, int64Ptr(0)); err != nil {
		return err
	}
	if job.ParentID != nil {
		if p, err := w.Store.GetV2Job(ctx, *job.ParentID); err == nil {
			if p.Status == "cancelled" {
				return nil
			}
			if p.Kind == store.KindBatch {
				w.deliver(ctx, p, "batch_scrape.page", map[string]any{"success": true, "data": []apiv2.Document{doc}, "scrapeId": job.ID.String()})
			}
		}
		return w.maybeCompleteBatch(ctx, *job.ParentID)
	}
	return nil
}

func (w *V2Worker) processCrawl(ctx context.Context, job *store.V2Job) error {
	w.deliver(ctx, job, "crawl.started", map[string]any{"success": true})
	start := time.Now()
	var req apiv2.CrawlRequest
	if err := json.Unmarshal(job.Request, &req); err != nil {
		return err
	}
	limit := 10000
	if req.Limit != nil {
		limit = *req.Limit
	}
	maxDepth := 100000
	if req.MaxDiscoveryDepth != nil {
		maxDepth = *req.MaxDiscoveryDepth
	} else if req.MaxDepth != nil {
		maxDepth = *req.MaxDepth
	}
	opts := scraper.DefaultOptions()
	opts.Timeout = 30 * time.Second
	extractTypes := []string{"markdown"}
	if req.ScrapeOptions != nil {
		if req.ScrapeOptions.Timeout > 0 {
			opts.Timeout = time.Duration(req.ScrapeOptions.Timeout) * time.Millisecond
		}
		extractTypes = extractTypesFor(req.ScrapeOptions.Formats)
		applyV2ScrapeOptions(opts, req.ScrapeOptions)
	}

	cancelled := func() bool {
		if ctx.Err() != nil {
			return true
		}
		cur, err := w.Store.GetV2Job(ctx, job.ID)
		return err == nil && cur.Status != "processing"
	}

	seedNorm, err := crawl.NormalizeURL(req.URL)
	if err != nil {
		return err
	}
	seedURL, err := url.Parse(seedNorm)
	if err != nil {
		return err
	}
	includeSubdomains := req.AllowSubdomains
	ignoreQuery := true
	if req.IgnoreQueryParameters != nil {
		ignoreQuery = *req.IgnoreQueryParameters
	}
	sameScope := func(u *url.URL) bool {
		if u == nil || seedURL == nil {
			return false
		}
		if strings.EqualFold(u.Hostname(), seedURL.Hostname()) {
			return true
		}
		if req.AllowExternalLinks || req.CrawlEntireDomain && req.AllowExternalLinks {
			return true
		}
		if includeSubdomains || req.CrawlEntireDomain {
			sh := strings.ToLower(seedURL.Hostname())
			uh := strings.ToLower(u.Hostname())
			return strings.HasSuffix(uh, "."+sh)
		}
		return false
	}
	pathAllowedCrawl := func(raw string) bool {
		target := raw
		regexOnFull := req.RegexOnFullURL != nil && *req.RegexOnFullURL
		if u, err := url.Parse(raw); err == nil && !regexOnFull {
			target = u.Path
		}
		for _, p := range req.ExcludePaths {
			if re, err := regexp.Compile(p); err == nil && re.MatchString(target) {
				return false
			}
		}
		if len(req.IncludePaths) == 0 {
			return true
		}
		for _, p := range req.IncludePaths {
			if re, err := regexp.Compile(p); err == nil && re.MatchString(target) {
				return true
			}
		}
		return false
	}
	normalize := func(raw string) (string, *url.URL, bool) {
		norm, err := crawl.NormalizeURL(raw)
		if err != nil {
			return "", nil, false
		}
		u, err := url.Parse(norm)
		if err != nil {
			return "", nil, false
		}
		if ignoreQuery {
			u.RawQuery = ""
			norm = u.String()
		}
		return norm, u, true
	}

	type frontierItem struct {
		URL   string
		Depth int
	}
	seen := map[string]bool{}
	queue := []frontierItem{}
	enqueue := func(raw string, depth int) {
		if depth < 0 || depth > maxDepth {
			return
		}
		norm, u, ok := normalize(raw)
		if !ok || seen[norm] {
			return
		}
		if !sameScope(u) || !pathAllowedCrawl(norm) {
			return
		}
		seen[norm] = true
		queue = append(queue, frontierItem{URL: norm, Depth: depth})
	}
	enqueue(seedNorm, 0)

	pagesProcessed := 0
	docs := make([]apiv2.Document, 0)
	for len(queue) > 0 && pagesProcessed < limit {
		if cancelled() {
			return nil
		}
		item := queue[0]
		queue = queue[1:]

		page, err := scraper.ScrapePage(ctx, item.URL, extractTypes, opts)
		pr := scraper.PageResult{URL: item.URL, Depth: item.Depth}
		if err != nil {
			if page != nil {
				pr.HTTPStatus = page.HTTPStatus
			}
		} else {
			pr.Links = page.Links
			pr.Headers = page.Headers
			pr.Paragraphs = page.Paragraphs
			pr.Markdown = page.Markdown
			pr.HTML = page.HTML
			pr.RawHTML = page.RawHTML
			pr.HTTPStatus = page.HTTPStatus
			pr.Truncated = page.Truncated
		}
		if cancelled() {
			return nil
		}

		doc := docFromPage(pr, req.ScrapeOptions)
		raw, _ := json.Marshal(doc)
		child, cErr := w.Store.CreateV2Child(ctx, job.ID, item.URL, item.Depth, job.Request)
		if cErr != nil {
			return cErr
		}
		status := "completed"
		var errMsg *string
		if pr.HTTPStatus == 0 {
			status = "failed"
			msg := "scrape failed"
			if err != nil {
				msg = err.Error()
			}
			errMsg = &msg
		}
		if uErr := w.Store.UpdateV2Job(ctx, child, status, raw, errMsg, &pr.HTTPStatus, 0, int64Ptr(0)); uErr != nil {
			return uErr
		}
		pagesProcessed++
		if status == "completed" {
			docs = append(docs, doc)
			w.deliver(ctx, job, "crawl.page", map[string]any{"success": true, "data": []apiv2.Document{doc}, "scrapeId": child.String()})
			if item.Depth < maxDepth && pagesProcessed < limit {
				for _, link := range pr.Links {
					enqueue(link, item.Depth+1)
				}
			}
		}
		if req.Delay > 0 {
			timer := time.NewTimer(time.Duration(req.Delay) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}

	if cancelled() {
		return nil
	}
	if err := w.Store.UpdateV2Job(ctx, job.ID, "completed", mustJSON(map[string]any{"pages_crawled": pagesProcessed}), nil, nil, 0, int64Ptr(time.Since(start).Milliseconds())); err != nil {
		return err
	}
	w.deliver(ctx, job, "crawl.completed", map[string]any{"success": true, "data": docs})
	return nil
}

func (w *V2Worker) processExtract(ctx context.Context, job *store.V2Job) error {
	w.deliver(ctx, job, "extract.started", map[string]any{"success": true})
	var req apiv2.ExtractRequest
	if err := json.Unmarshal(job.Request, &req); err != nil {
		return err
	}
	if !w.LLM || w.Extractor == nil {
		return fmt.Errorf("extract is not configured")
	}
	out := make([]json.RawMessage, len(req.URLs))
	for i := range out {
		out[i] = json.RawMessage("null")
	}
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i, rawURL := range req.URLs {
		i, rawURL := i, rawURL
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			opts := apiv2.DefaultScrapeOptions()
			if req.ScrapeOptions != nil {
				opts = *req.ScrapeOptions
			}
			doc, err := w.scrape(ctx, apiv2.ScrapeRequest{URL: rawURL, ScrapeOptions: opts})
			if err != nil {
				return
			}
			raw, err := w.Extractor.Extract(ctx, doc.Markdown, rawSchema(req.Schema), req.Prompt)
			if err != nil {
				return
			}
			mu.Lock()
			out[i] = raw
			successes++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if successes == 0 {
		return fmt.Errorf("all extract URLs failed")
	}
	var payload json.RawMessage
	if len(req.URLs) == 1 {
		payload = out[0]
	} else {
		payload = mustJSON(out)
	}
	if err := w.Store.UpdateV2Job(ctx, job.ID, "completed", payload, nil, nil, 0, int64Ptr(0)); err != nil {
		return err
	}
	var extractData any
	_ = json.Unmarshal(payload, &extractData)
	w.deliver(ctx, job, "extract.completed", extractData)
	return nil
}

func (w *V2Worker) maybeCompleteBatch(ctx context.Context, parent uuid.UUID) error {
	_, processing, queued, total, err := w.Store.V2ChildCounts(ctx, parent)
	if err != nil {
		return err
	}
	if total == 0 || processing > 0 || queued > 0 {
		return nil
	}
	p, err := w.Store.GetV2Job(ctx, parent)
	if err != nil {
		return err
	}
	if p.Status == "cancelled" || p.Status == "completed" || p.Status == "failed" {
		return nil
	}
	if err := w.Store.UpdateV2Job(ctx, parent, "completed", nil, nil, nil, 0, int64Ptr(0)); err != nil {
		return err
	}
	children, _ := w.Store.ListV2Children(ctx, parent, "completed", 100, 0)
	docs := make([]apiv2.Document, 0, len(children))
	for _, ch := range children {
		if len(ch.Result) == 0 {
			continue
		}
		var doc apiv2.Document
		if json.Unmarshal(ch.Result, &doc) == nil {
			docs = append(docs, doc)
		}
	}
	w.deliver(ctx, p, "batch_scrape.completed", map[string]any{"success": true, "data": docs})
	return nil
}

func (w *V2Worker) reaper(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick++
			if tick%20 == 0 {
				retention := w.RetentionMinutes
				if retention < 1 {
					retention = 1440
				}
				_ = w.Store.CleanupV2(ctx, retention)
			}
			jobs, err := w.Store.ReapV2Leases(ctx)
			if err != nil {
				slog.Error("reap leases", "error", err)
				continue
			}
			for _, j := range jobs {
				payload := queue.StreamPayload{JobID: j.ID.String(), Kind: j.Kind, URL: j.URL, Depth: j.Depth}
				if j.ParentID != nil {
					payload.ParentID = j.ParentID.String()
				}
				stream := queue.StreamDefault
				if j.Kind == store.KindPage && strings.Contains(j.URL, "browser") {
					stream = queue.StreamBrowser
				}
				_ = w.Queue.PublishStream(ctx, stream, payload)
			}
		}
	}
}

func (w *V2Worker) scrape(ctx context.Context, req apiv2.ScrapeRequest) (apiv2.Document, error) {
	opts := scraper.DefaultOptions()
	if req.Timeout > 0 {
		opts.Timeout = time.Duration(req.Timeout) * time.Millisecond
	}
	if len(req.Headers) > 0 {
		opts.Headers = req.Headers
	}
	opts.OnlyMainContent = req.OnlyMainContent
	opts.IncludeTags = req.IncludeTags
	opts.ExcludeTags = req.ExcludeTags
	types := extractTypesFor(req.Formats)
	res, err := scraper.ScrapePage(ctx, req.URL, types, opts)
	if err != nil {
		return apiv2.Document{}, err
	}
	finalURL := req.URL
	if res.FinalURL != "" {
		finalURL = res.FinalURL
	}
	return apiv2.Document{
		Markdown: res.Markdown, HTML: res.HTML, RawHTML: res.RawHTML, Links: res.Links,
		Metadata: apiv2.Metadata{SourceURL: req.URL, URL: finalURL, StatusCode: res.HTTPStatus, ContentType: res.ContentType},
	}, nil
}

func applyV2ScrapeOptions(dst *scraper.ScrapeOptions, src *apiv2.ScrapeOptions) {
	if dst == nil || src == nil {
		return
	}
	if len(src.Headers) > 0 {
		dst.Headers = src.Headers
	}
	dst.OnlyMainContent = src.OnlyMainContent
	dst.IncludeTags = src.IncludeTags
	dst.ExcludeTags = src.ExcludeTags
}

func extractTypesFor(formats apiv2.Formats) []string {
	if len(formats) == 0 {
		return []string{"markdown"}
	}
	var out []string
	for _, f := range formats {
		switch f.Type {
		case "markdown":
			out = append(out, "markdown")
		case "html":
			out = append(out, "html")
		case "rawHtml":
			out = append(out, "raw_html")
		case "links":
			out = append(out, "links")
		}
	}
	if len(out) == 0 {
		out = []string{"markdown"}
	}
	return out
}

func docFromPage(page scraper.PageResult, opts *apiv2.ScrapeOptions) apiv2.Document {
	return apiv2.Document{
		Markdown: page.Markdown, HTML: page.HTML, RawHTML: page.RawHTML, Links: page.Links,
		Metadata: apiv2.Metadata{URL: page.URL, SourceURL: page.URL, StatusCode: page.HTTPStatus},
	}
}

func rawSchema(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, _ := json.Marshal(v)
	return data
}

func (w *V2Worker) deliver(ctx context.Context, job *store.V2Job, event string, payload any) {
	if len(job.Webhook) == 0 {
		return
	}
	var hook apiv2.Webhook
	if err := json.Unmarshal(job.Webhook, &hook); err != nil || hook.URL == "" {
		return
	}
	if !webhookEventEnabled(hook.Events, event) {
		return
	}
	meta := hook.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	body, _ := json.Marshal(map[string]any{
		"success": true, "type": event, "id": job.ID.String(), "data": payload, "metadata": meta,
	})
	if err := scraper.ValidatePublicURL(ctx, hook.URL); err != nil {
		metrics.JobsTotal.WithLabelValues("webhook_failed").Inc()
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hook.Headers {
		req.Header.Set(k, v)
	}
	if w.WebhookSecret != "" {
		mac := hmac.New(sha256.New, []byte(w.WebhookSecret))
		mac.Write(body)
		req.Header.Set("X-Webhook-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			if err := scraper.ValidatePublicURL(ctx, hook.URL); err != nil {
				metrics.JobsTotal.WithLabelValues("webhook_failed").Inc()
				return
			}
		}
		attemptReq, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
		if err != nil {
			return
		}
		attemptReq.Header = req.Header.Clone()
		res, err := client.Do(attemptReq)
		if err == nil {
			res.Body.Close()
			if res.StatusCode >= 200 && res.StatusCode < 300 {
				metrics.JobsTotal.WithLabelValues("webhook").Inc()
				return
			}
			if res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != http.StatusTooManyRequests {
				metrics.JobsTotal.WithLabelValues("webhook_failed").Inc()
				return
			}
		}
		if attempt < 3 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
	}
	metrics.JobsTotal.WithLabelValues("webhook_failed").Inc()
}

func webhookEventEnabled(events []string, event string) bool {
	kind := "completed"
	if i := strings.LastIndex(event, "."); i >= 0 {
		kind = event[i+1:]
	}
	if len(events) == 0 {
		return true
	}
	for _, e := range events {
		if e == kind {
			return true
		}
	}
	return false
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func int64Ptr(n int64) *int64 { return &n }
