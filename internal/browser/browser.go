package browser

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/chromedp"
)

type Page struct {
	URL        string
	Title      string
	Content    string
	Elements   []Element
	Screenshot []byte
}

type Element struct {
	Ref      int    `json:"ref"`
	Tag      string `json:"tag"`
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Selector string `json:"selector"`
	Href     string `json:"href,omitempty"`
	Src      string `json:"src,omitempty"`
	Alt      string `json:"alt,omitempty"`
	Name     string `json:"name,omitempty"`
	Value    string `json:"value,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Checked  bool   `json:"checked,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Rect     Rect   `json:"rect,omitempty"`
}

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type Browser struct {
	ctx         context.Context
	cancel      context.CancelFunc
	allocCtx    context.Context
	allocCancel context.CancelFunc
}

func New(headless bool) (*Browser, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"),
	)

	if p := os.Getenv("LWP_CHROME_PATH"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	return &Browser{
		ctx:         ctx,
		cancel:      cancel,
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
	}, nil
}

func (b *Browser) Close() {
	b.cancel()
	b.allocCancel()
}

func (b *Browser) Navigate(url string, timeout time.Duration) (*Page, error) {
	ctx, cancel := context.WithTimeout(b.ctx, timeout)
	defer cancel()

	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
	); err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}

	return b.extractPage(ctx)
}

func (b *Browser) extractPage(ctx context.Context) (*Page, error) {
	page := &Page{}

	var title, url string
	var elementsJSON string

	extractJS := `
		(() => {
			const items = [];
			const seen = new Set();
			let ref = 0;
			const tags = document.querySelectorAll('a, button, input, select, textarea, img, canvas, [role="button"], [tabindex]:not([tabindex="-1"])');
			tags.forEach(el => {
				const tag = el.tagName.toLowerCase();
				const rect = el.getBoundingClientRect();
				if (rect.width === 0 || rect.height === 0) return;
				const selector = getSelector(el);
				if (seen.has(selector)) return;
				seen.add(selector);
				ref++;
				const item = {
					ref: ref, tag: tag, selector: selector,
					text: (el.textContent || '').trim().slice(0, 200),
					type: el.type || '',
					name: el.name || '',
					value: el.value || '',
					placeholder: el.placeholder || '',
					href: el.href || '',
					src: el.src || '',
					alt: el.alt || '',
					checked: !!el.checked,
					disabled: !!el.disabled,
					rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
				};
				items.push(item);
			});
			return JSON.stringify(items);

			function getSelector(el) {
				if (el.id) return '#' + CSS.escape(el.id);
				let path = [];
				while (el && el.nodeType === 1) {
					let tag = el.tagName.toLowerCase();
					let sibling = el;
					let same = 0;
					while (sibling) {
						if (sibling.tagName && sibling.tagName.toLowerCase() === tag) same++;
						sibling = sibling.previousElementSibling;
					}
					if (el.id) { path.unshift('#' + CSS.escape(el.id)); break; }
					path.unshift(tag + (same > 1 ? ':nth-of-type(' + same + ')' : ''));
					el = el.parentElement;
				}
				return path.join(' > ');
			}
		})()
	`

	tasks := chromedp.Tasks{
		chromedp.Title(&title),
		chromedp.Location(&url),
		chromedp.Evaluate(extractJS, &elementsJSON),
	}

	if err := chromedp.Run(ctx, tasks); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	page.Title = title
	page.URL = url

	if err := jsonUnmarshal([]byte(elementsJSON), &page.Elements); err != nil {
		return nil, fmt.Errorf("parse elements: %w", err)
	}

	var textContent string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.body.innerText`, &textContent)); err == nil {
		page.Content = textContent
	}

	return page, nil
}

func (b *Browser) extractPageFull(ctx context.Context) (*Page, error) {
	page, err := b.extractPage(ctx)
	if err != nil {
		return nil, err
	}

	var screenshot []byte
	if err := chromedp.Run(ctx, chromedp.FullScreenshot(&screenshot, 90)); err == nil {
		page.Screenshot = screenshot
	}

	return page, nil
}

func (b *Browser) Refresh() (*Page, error) {
	ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.Reload(), chromedp.WaitReady("body"), chromedp.Sleep(1*time.Second)); err != nil {
		return nil, err
	}

	return b.extractPage(ctx)
}

func (b *Browser) ReExtract() (*Page, error) {
	ctx, cancel := context.WithTimeout(b.ctx, 15*time.Second)
	defer cancel()
	return b.extractPage(ctx)
}

func (b *Browser) Click(selector string) error {
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
	)
}

func (b *Browser) Type(selector, text string) error {
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Clear(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
	)
}

func (b *Browser) Submit(selector string) error {
	ctx, cancel := context.WithTimeout(b.ctx, 15*time.Second)
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.Submit(selector, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
	)
}

func (b *Browser) ScreenshotElement(selector string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()

	var buf []byte
	err := chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var rect []int
			js := fmt.Sprintf(`(() => {
				const el = document.querySelector(%q);
				if (!el) return null;
				const r = el.getBoundingClientRect();
				return [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)];
			})()`, selector)
			if err := chromedp.Evaluate(js, &rect).Do(ctx); err != nil {
				return err
			}
			if len(rect) != 4 || rect[2] == 0 || rect[3] == 0 {
				return fmt.Errorf("element not visible or zero-size")
			}
			return chromedp.FullScreenshot(&buf, 90).Do(ctx)
		}),
	)
	if err != nil {
		return nil, err
	}
	// crop the full screenshot to just the element bounds
	// for now we return full screenshot; caller can use Gemini Vision
	return buf, nil
}

func (b *Browser) ScreenshotFull() ([]byte, error) {
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()

	var buf []byte
	err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90))
	return buf, err
}

func (b *Browser) Evaluate(js string, result interface{}) error {
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()

	return chromedp.Run(ctx, chromedp.Evaluate(js, result))
}
