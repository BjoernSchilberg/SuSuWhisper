package main

import "github.com/microcosm-cc/bluemonday"

// policy allows the HTML that TinyMCE produces (paragraphs, headings, lists,
// tables, links, images) and removes everything that could run code, such as
// <script>, event handlers and javascript: URLs.
var policy = newPolicy()

func newPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Inline styles set by the toolbar: alignment, highlight colour, indent.
	p.AllowStyles("text-align", "background-color", "padding-left").Globally()
	return p
}

func sanitizeHTML(s string) string {
	return policy.Sanitize(s)
}
