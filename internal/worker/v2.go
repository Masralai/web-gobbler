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
	"strings"
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
		return err
	}
	doc.Metadata.ScrapeID = job.ID.String()
	raw, _ := json.Marshal(doc)
	if err := w.Store.UpdateV2Job(ctx, job.ID, "completed", raw, nil, &doc.Metadata.StatusCode, 0, int64Ptr(0)); err != nil {
		return err
	}
	if job.ParentID != nil {
		return w.maybeCompleteBatch(ctx, *job.ParentID)
	}
	return nil
}

func (w *V2Worker) processCrawl(ctx context.Context, job *store.V2Job) error {
	var req apiv2.CrawlRequest
	if err := json.Unmarshal(job.Request, &req); err != nil {
		return err
	}
	limit := 10000
	if req.Limit != nil {
		limit = *req.Limit
	}
	depth := 0
	if req.MaxDiscoveryDepth != nil {
		depth = *req.MaxDiscoveryDepth
	} else if req.MaxDepth != nil {
		depth = *req.MaxDepth
	} else {
		depth = 100000
	}
	opts := scraper.DefaultOptions()
	opts.Timeout = 30 * time.Second
	extractTypes := []string{"markdown"}
	if req.ScrapeOptions != nil {
		if req.ScrapeOptions.Timeout > 0 {
			opts.Timeout = time.Duration(req.ScrapeOptions.Timeout) * time.Millisecond
		}
		extractTypes = extractTypesFor(req.ScrapeOptions.Formats)
	}
	cancel := func() bool {
		cur, err := w.Store.GetV2Job(ctx, job.ID)
		return err == nil && cur.Status != "processing"
	}
	res, err := crawl.CrawlBFS(ctx, req.URL, crawl.Options{
		MaxPages: limit, MaxDepth: depth, Extract: extractTypes, Scrape: *opts,
	}, nil, cancel)
	if err != nil {
		return err
	}
	for _, page := range res.Pages {
		doc := docFromPage(page, req.ScrapeOptions)
		raw, _ := json.Marshal(doc)
		child, err := w.Store.CreateV2Child(ctx, job.ID, page.URL, page.Depth, job.Request)
		if err != nil {
			return err
		}
		status := "completed"
		var errMsg *string
		if page.HTTPStatus == 0 {
			status = "failed"
			msg := "scrape failed"
			errMsg = &msg
		}
		if err := w.Store.UpdateV2Job(ctx, child, status, raw, errMsg, &page.HTTPStatus, 0, int64Ptr(0)); err != nil {
			return err
		}
	}
	if err := w.Store.UpdateV2Job(ctx, job.ID, "completed", mustJSON(res), nil, nil, 0, int64Ptr(res.DurationMs)); err != nil {
		return err
	}
	w.deliver(ctx, job, "crawl.completed", mustJSON(res))
	return nil
}

func (w *V2Worker) processExtract(ctx context.Context, job *store.V2Job) error {
	var req apiv2.ExtractRequest
	if err := json.Unmarshal(job.Request, &req); err != nil {
		return err
	}
	if !w.LLM || w.Extractor == nil {
		return fmt.Errorf("extract is not configured")
	}
	out := make([]json.RawMessage, 0, len(req.URLs))
	successes := 0
	for _, rawURL := range req.URLs {
		opts := apiv2.DefaultScrapeOptions()
		if req.ScrapeOptions != nil {
			opts = *req.ScrapeOptions
		}
		doc, err := w.scrape(ctx, apiv2.ScrapeRequest{URL: rawURL, ScrapeOptions: opts})
		if err != nil {
			out = append(out, json.RawMessage("null"))
			continue
		}
		raw, err := w.Extractor.Extract(ctx, doc.Markdown, rawSchema(req.Schema), req.Prompt)
		if err != nil {
			out = append(out, json.RawMessage("null"))
			continue
		}
		out = append(out, raw)
		successes++
	}
	if successes == 0 {
		return fmt.Errorf("all extract URLs failed")
	}
	raw, _ := json.Marshal(out)
	if err := w.Store.UpdateV2Job(ctx, job.ID, "completed", raw, nil, nil, 0, int64Ptr(0)); err != nil {
		return err
	}
	w.deliver(ctx, job, "extract.completed", raw)
	return nil
}

func (w *V2Worker) maybeCompleteBatch(ctx context.Context, parent uuid.UUID) error {
	completed, processing, queued, total, err := w.Store.V2ChildCounts(ctx, parent)
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
	w.deliver(ctx, p, "batch_scrape.completed", mustJSON(map[string]any{"completed": completed, "total": total}))
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

func (w *V2Worker) deliver(ctx context.Context, job *store.V2Job, event string, data json.RawMessage) {
	if len(job.Webhook) == 0 {
		return
	}
	var hook apiv2.Webhook
	if err := json.Unmarshal(job.Webhook, &hook); err != nil || hook.URL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"success": true, "type": event, "id": job.ID.String(), "data": json.RawMessage(data),
	})
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
		req.Header.Set("x-firecrawl-signature", hex.EncodeToString(mac.Sum(nil)))
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for attempt := 0; attempt < 4; attempt++ {
		attemptReq, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
		if err != nil {
			return
		}
		attemptReq.Header = req.Header.Clone()
		res, err := client.Do(attemptReq)
		if err == nil {
			res.Body.Close()
			if res.StatusCode < 500 && res.StatusCode != http.StatusTooManyRequests {
				metrics.JobsTotal.WithLabelValues("webhook").Inc()
				return
			}
		}
		if attempt < 3 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
	}
	metrics.JobsTotal.WithLabelValues("webhook_failed").Inc()
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func int64Ptr(n int64) *int64 { return &n }
