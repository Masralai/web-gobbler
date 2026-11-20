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
	"sort"
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
	MaxConcurrent    int
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
	max := w.MaxConcurrent
	if max <= 0 {
		max = 5
	}
	leaseMember := "job:" + id.String()
	if w.Queue != nil {
		ok, err := w.Queue.ConcurrencyAcquire(ctx, leaseMember, max, 90*time.Second)
		if err != nil {
			return err
		}
		if !ok {
			// ponytail: no cluster slot; leave unacked for reclaim after idle timeout.
			return fmt.Errorf("concurrency limit reached")
		}
	}
	release := func() {
		if w.Queue != nil {
			_ = w.Queue.ConcurrencyRelease(context.Background(), leaseMember)
		}
	}
	stopRenew := make(chan struct{})
	if w.Queue != nil {
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-stopRenew:
					return
				case <-t.C:
					_ = w.Queue.ConcurrencyRenew(context.Background(), leaseMember, 90*time.Second)
				}
			}
		}()
	}
	claimed, err := w.Store.SetV2Processing(ctx, id)
	if err != nil {
		close(stopRenew)
		release()
		return err
	}
	if !claimed {
		close(stopRenew)
		release()
		return nil
	}
	job, err := w.Store.GetV2Job(ctx, id)
	if err != nil {
		close(stopRenew)
		release()
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
		close(stopRenew)
		release()
		return err
	}
	close(stopRenew)
	release()
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
	ignoreQuery := true
	if req.IgnoreQueryParameters != nil {
		ignoreQuery = *req.IgnoreQueryParameters
	}
	scope := crawl.Scope{
		Seed:                  seedURL,
		AllowSubdomains:       req.AllowSubdomains,
		CrawlEntireDomain:     req.CrawlEntireDomain,
		AllowExternalLinks:    req.AllowExternalLinks,
		IncludePaths:          req.IncludePaths,
		ExcludePaths:          req.ExcludePaths,
		RegexOnFullURL:        req.RegexOnFullURL != nil && *req.RegexOnFullURL,
		IgnoreQueryParameters: ignoreQuery,
	}

	robots, _ := crawl.FetchRobots(ctx, seedNorm, *opts)
	robotsBlocked := map[string]bool{}
	allowedByRobots := func(raw string) bool {
		if req.IgnoreRobotsTxt || robots == nil {
			return true
		}
		if robots.Allowed(raw) {
			return true
		}
		robotsBlocked[raw] = true
		return false
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
		norm, _, ok := crawl.NormalizeCrawlURL(raw, ignoreQuery)
		if !ok || seen[norm] {
			return
		}
		if !scope.Allowed(norm) || !allowedByRobots(norm) {
			return
		}
		seen[norm] = true
		queue = append(queue, frontierItem{URL: norm, Depth: depth})
	}
	sitemapMode := strings.ToLower(strings.TrimSpace(req.Sitemap))
	if sitemapMode == "" {
		sitemapMode = "include"
	}
	if sitemapMode == "include" || sitemapMode == "only" {
		if smURLs, err := crawl.DiscoverSitemapURLs(ctx, seedNorm, sitemapMode, robots, *opts, limit); err == nil {
			for _, u := range smURLs {
				enqueue(u, 0)
			}
		}
	}
	if sitemapMode != "only" {
		enqueue(seedNorm, 0)
	}

	pagesProcessed := 0
	docs := make([]apiv2.Document, 0)
	for len(queue) > 0 && pagesProcessed < limit {
		if cancelled() {
			return nil
		}
		item := queue[0]
		queue = queue[1:]

		var (
			page        *scraper.Result
			err         error
			attempts    int
			retriesUsed int
		)
		for attempt := 1; attempt <= 6; attempt++ {
			attempts = attempt
			page, err = scraper.ScrapePage(ctx, item.URL, extractTypes, opts)
			statusCode := 0
			if page != nil {
				statusCode = page.HTTPStatus
			}
			if err == nil && statusCode >= 200 && statusCode < 300 {
				break
			}
			retryable := crawlPageRetryable(statusCode, err)
			if !retryable || attempt == 6 || cancelled() {
				break
			}
			retriesUsed = attempt
			timer := time.NewTimer(time.Duration(attempt) * 200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		pr := scraper.PageResult{URL: item.URL, Depth: item.Depth}
		if page != nil {
			pr.Links = page.Links
			pr.Headers = page.Headers
			pr.Paragraphs = page.Paragraphs
			pr.Markdown = page.Markdown
			pr.HTML = page.HTML
			pr.RawHTML = page.RawHTML
			pr.HTTPStatus = page.HTTPStatus
			pr.Truncated = page.Truncated
			pr.Title = page.Title
			pr.Description = page.Description
			pr.Language = page.Language
			pr.Keywords = page.Keywords
			pr.RobotsMeta = page.RobotsMeta
			pr.OgImage = page.OgImage
			pr.Favicon = page.Favicon
		}
		if attempts > 1 && retriesUsed < attempts-1 {
			retriesUsed = attempts - 1
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
		if pr.HTTPStatus < 200 || pr.HTTPStatus >= 300 {
			status = "failed"
			msg := "scrape failed"
			if err != nil {
				msg = err.Error()
			} else if pr.HTTPStatus != 0 {
				msg = fmt.Sprintf("scrape failed with status %d", pr.HTTPStatus)
			}
			errMsg = &msg
		}
		if uErr := w.Store.UpdateV2Job(ctx, child, status, raw, errMsg, &pr.HTTPStatus, retriesUsed, int64Ptr(0)); uErr != nil {
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
	completedPayload := map[string]any{"pages_crawled": pagesProcessed}
	if len(robotsBlocked) > 0 {
		rb := make([]string, 0, len(robotsBlocked))
		for u := range robotsBlocked {
			rb = append(rb, u)
		}
		sort.Strings(rb)
		completedPayload["robotsBlocked"] = rb
	}
	if err := w.Store.UpdateV2Job(ctx, job.ID, "completed", mustJSON(completedPayload), nil, nil, 0, int64Ptr(time.Since(start).Milliseconds())); err != nil {
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
	_, processing, queued, all, err := w.Store.V2ChildCounts(ctx, parent)
	if err != nil {
		return err
	}
	if all == 0 || processing > 0 || queued > 0 {
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
			w.dispatchOutbox(ctx)
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

func (w *V2Worker) dispatchOutbox(ctx context.Context) {
	if w.Store == nil || w.Queue == nil {
		return
	}
	rows, err := w.Store.ListUnsentOutbox(ctx, 100)
	if err != nil {
		return
	}
	for _, r := range rows {
		var p queue.StreamPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			_ = w.Store.MarkOutboxSent(ctx, r.ID)
			continue
		}
		if err := w.Queue.PublishStream(ctx, r.Stream, p); err != nil {
			return
		}
		_ = w.Store.MarkOutboxSent(ctx, r.ID)
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
		Metadata: apiv2.Metadata{
			SourceURL: req.URL, URL: finalURL, StatusCode: res.HTTPStatus, ContentType: res.ContentType,
			Title: res.Title, Description: res.Description, Language: res.Language,
			Keywords: res.Keywords, Robots: res.RobotsMeta, OgImage: res.OgImage, Favicon: res.Favicon,
		},
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
		Metadata: apiv2.Metadata{
			URL: page.URL, SourceURL: page.URL, StatusCode: page.HTTPStatus,
			Title: page.Title, Description: page.Description, Language: page.Language,
			Keywords: page.Keywords, Robots: page.RobotsMeta, OgImage: page.OgImage, Favicon: page.Favicon,
		},
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

func crawlPageRetryable(status int, err error) bool {
	if status == 429 || status >= 500 {
		return true
	}
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "temporar") ||
		strings.Contains(msg, "connection") ||
		strings.Contains(msg, "reset") ||
		strings.Contains(msg, "refused") ||
		strings.Contains(msg, "eof") ||
		strings.Contains(msg, "dns") ||
		strings.Contains(msg, "request failed") ||
		strings.Contains(msg, "context deadline")
}
