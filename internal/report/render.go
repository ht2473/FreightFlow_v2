package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
)

// Page — содержимое страницы отчёта: заголовок и таблицы для XLSX, которые страница печати
// выкладывает в window.__ffReport вместе с признаком готовности.
type Page struct {
	Title   string       `json:"title"`
	Sheets  []Sheet      `json:"sheets"`
	Sources []PageSource `json:"sources"`
}

// Sheet — таблица отчёта.
type Sheet struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// PageSource — источник данных отчёта для атрибуции.
type PageSource struct {
	Title       string `json:"title"`
	Attribution string `json:"attribution"`
	License     string `json:"license"`
}

// Rendered — результат рендеринга: PDF (для формата pdf) и содержимое страницы.
type Rendered struct {
	PDF  []byte
	Page Page
}

// Renderer открывает страницу отчёта от имени пользователя и печатает её.
type Renderer interface {
	Render(ctx context.Context, path, token string, pdf bool) (*Rendered, error)
}

// Chrome — рендеринг в службе pdf (headless-shell) по протоколу DevTools. Страница отчёта —
// раздел печати приложения: те же графики и карты, что в интерфейсе (Р-14).
type Chrome struct {
	// CDP — адрес DevTools службы pdf (ws://pdf:9222).
	CDP string
	// Base — адрес приложения, как его видит служба pdf (http://app:8080).
	Base string
	// Timeout — предел на страницу.
	Timeout time.Duration
}

// errNotReady — страница не сообщила о готовности за отведённое время.
var errNotReady = errors.New("report: page did not become ready")

// Render открывает страницу path с токеном печати в заголовке Authorization, ждёт признак
// готовности (data-ready на body) и возвращает PDF или содержимое страницы.
func (c *Chrome) Render(ctx context.Context, path, token string, pdf bool) (*Rendered, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	// Docker Desktop публикует порт службы только на IPv4, а localhost на Windows сначала ::1.
	cdpURL := strings.Replace(c.CDP, "//localhost:", "//127.0.0.1:", 1)
	actx, acancel := remote.NewAllocator(ctx, cdpURL)
	defer acancel()
	bctx, bcancel := chromedp.NewContext(actx)
	defer bcancel()

	setup := func(ctx context.Context, t *chromedp.Target) (chromedp.Void, error) {
		if _, err := cdp.Call(ctx, t, network.Enable, network.EnableParams{}); err != nil {
			return chromedp.Void{}, err
		}
		if _, err := cdp.Call(ctx, t, network.SetExtraHTTPHeaders, network.SetExtraHTTPHeadersParams{
			Headers: network.Headers{"Authorization": "Bearer " + token},
		}); err != nil {
			return chromedp.Void{}, err
		}
		// Графики без анимации и светлая тема: печатается конечное состояние.
		if _, err := cdp.Call(ctx, t, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{
			Features: []*emulation.MediaFeature{{Name: "prefers-reduced-motion", Value: "reduce"}, {Name: "prefers-color-scheme", Value: "light"}},
		}); err != nil {
			return chromedp.Void{}, err
		}
		// Ширина страницы A4 при 96 точках на дюйм: графики и карты размечаются под печать.
		_, err := cdp.Call(ctx, t, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
			Width: 794, Height: 1123, DeviceScaleFactor: 2, Mobile: false,
		})
		return chromedp.Void{}, err
	}
	if err := chromedp.Do(bctx, setup); err != nil {
		return nil, fmt.Errorf("report: browser setup: %w", err)
	}
	if err := chromedp.Do(bctx, chromedp.Navigate(strings.TrimRight(c.Base, "/")+path)); err != nil {
		return nil, fmt.Errorf("report: open page: %w", err)
	}
	for {
		state, err := chromedp.Run(bctx, chromedp.Evaluate[string](`document.body ? (document.body.dataset.ready || "") : ""`))
		if err != nil {
			return nil, fmt.Errorf("report: page state: %w", err)
		}
		if state == "true" {
			break
		}
		if state == "error" {
			msg, _ := chromedp.Run(bctx, chromedp.Evaluate[string](`document.body.dataset.error || "ошибка страницы отчёта"`))
			return nil, fmt.Errorf("report: %s", msg)
		}
		if time.Until(deadline) < 5*time.Second {
			// Что видно на странице — для разбора: адрес и начало текста.
			info, _ := chromedp.Run(bctx, chromedp.Evaluate[string](`location.href + " | " + (document.body ? document.body.innerText.slice(0, 300) : "")`))
			return nil, fmt.Errorf("%w: %s", errNotReady, strings.Join(strings.Fields(info), " "))
		}
		select {
		case <-bctx.Done():
			return nil, errNotReady
		case <-time.After(300 * time.Millisecond):
		}
	}
	raw, err := chromedp.Run(bctx, chromedp.Evaluate[string](`JSON.stringify(window.__ffReport || {})`))
	if err != nil {
		return nil, fmt.Errorf("report: page data: %w", err)
	}
	out := &Rendered{}
	if err := json.Unmarshal([]byte(raw), &out.Page); err != nil {
		return nil, fmt.Errorf("report: decode page data: %w", err)
	}
	if !pdf {
		return out, nil
	}
	out.PDF, err = chromedp.Run(bctx, chromedp.PrintToPDF(
		chromedp.PDFPaper(chromedp.PaperA4),
		chromedp.PDFMargin(0.45),
		chromedp.PDFPrintBackground(),
		chromedp.PDFHeaderTemplate(`<div></div>`),
		chromedp.PDFFooterTemplate(`<div style="width:100%;font-size:7px;color:#6b6f80;padding:0 12mm;display:flex;justify-content:space-between;font-family:Arial,sans-serif"><span>FreightFlow · <span class="title"></span></span><span><span class="pageNumber"></span> / <span class="totalPages"></span></span></div>`),
	))
	if err != nil {
		return nil, fmt.Errorf("report: print: %w", err)
	}
	return out, nil
}
