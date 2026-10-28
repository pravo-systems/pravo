package main

import "html/template"

// The page shells. docs/ pages, the landing page, the contents page and
// redirect stubs are described in HTML.md; keep the two in step.
const shellTemplates = `
{{define "head"}}<!doctype html>
<html lang="en" class="no-js">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
{{if .Description}}<meta name="description" content="{{.Description}}">
{{end}}{{if .NoIndex}}<meta name="robots" content="noindex">
{{end}}<meta name="color-scheme" content="light dark">
<meta name="theme-color" content="#f5f2ea" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#0b0d10" media="(prefers-color-scheme: dark)">
<meta property="og:site_name" content="{{.SiteName}}">
<meta property="og:title" content="{{.OGTitle}}">
{{if .Description}}<meta property="og:description" content="{{.Description}}">
{{end}}<meta property="og:type" content="{{.OGType}}">
{{if .Canonical}}<meta property="og:url" content="{{.Canonical}}">
{{if .OGImage}}<meta property="og:image" content="{{.OGImage}}">
{{end}}<meta name="twitter:card" content="{{if .OGImage}}summary_large_image{{else}}summary{{end}}">
{{end}}{{if .ICO}}<link rel="icon" href="{{.Root}}favicon.ico" sizes="any">
{{else}}<link rel="icon" type="image/svg+xml" href="{{.Root}}assets/logo.svg">
{{end}}{{if .TouchIcon}}<link rel="apple-touch-icon" href="{{.Root}}assets/apple-touch-icon.png">
{{end}}{{if .Canonical}}<link rel="canonical" href="{{.Canonical}}">
{{end}}{{if .TwinHref}}<link rel="alternate" type="text/markdown" href="{{.TwinHref}}">
{{end}}{{range .Preload}}<link rel="preload" href="{{.}}" as="font" type="font/woff2" crossorigin>
{{end}}<script>
(function () {
  var d = document.documentElement;
  d.className = d.className.replace("no-js", "js");
  try {
    var t = localStorage.getItem("pravo-theme");
    if (t === "light" || t === "dark") d.setAttribute("data-theme", t);
  } catch (e) {}
})();
</script>
<link rel="stylesheet" href="{{.Root}}assets/style.css">
<script src="{{.Root}}assets/site.js" defer></script>
{{if .Widgets}}<script src="{{.Root}}assets/widgets.js" defer></script>
{{end}}</head>
{{end}}

{{define "header"}}<a class="skip-link" href="#content">Skip to content</a>
<header class="site-header">
<a class="brand" href="{{.BrandHref}}"><img class="brand-mark" src="{{.LogoSrc}}" alt="" width="24" height="24"> <span class="brand-name">{{.BrandName}}</span></a>
<nav class="site-nav" aria-label="Documentation sections">
{{range .Nav}}<a href="{{.Href}}"{{if .Current}} aria-current="{{if .IsPage}}page{{else}}true{{end}}"{{end}}>{{.Title}}</a>
{{end}}</nav>
<div class="site-tools">
{{if .RepoHref}}<a class="repo-link" href="{{.RepoHref}}" rel="noopener">{{.RepoLabel}}</a>
{{end}}<button class="search-open" type="button">Search <kbd>/</kbd></button>
<button id="theme-toggle" type="button" aria-label="Toggle colour theme"></button>
<a class="btn btn-primary" href="{{.StartHref}}">{{.StartLabel}}</a>
</div>
</header>
{{end}}

{{define "footer"}}<footer class="site-footer">
<div class="foot-inner">
<div class="foot-row">
<p class="foot-brand"><span class="foot-title">{{.Name}}</span></p>
<nav class="foot-nav" aria-label="Footer">
<ul class="foot-sections">
{{range .Sections}}<li><a href="{{.Href}}">{{.Title}}</a></li>
{{end}}</ul>
<ul class="foot-project">
{{range .Project}}<li><a href="{{.Href}}"{{if .External}} rel="noopener"{{end}}>{{.Title}}</a></li>
{{end}}</ul>
</nav>
</div>
{{if .Note}}<p class="legal">{{if .Applies}}<span class="applies">{{.Applies}}</span> · {{end}}{{.Note}}</p>
{{end}}
</div>
</footer>
{{end}}

{{define "doc"}}{{template "head" .Head}}<body class="page page-doc" data-section="{{.Section}}" data-root="{{.Root}}">
{{template "header" .Header}}
<div class="doc-shell{{if not .TOC}} no-toc{{end}}{{if not .Sidebar}} no-sidebar{{end}}">
{{if .Sidebar}}<div class="doc-sidebar" id="sidebar">
<nav aria-label="Pages in this section">
{{if .Pinned}}<ul class="side-pinned">
{{range .Pinned}}<li><a href="{{.Href}}">{{.Title}}</a></li>
{{end}}</ul>
{{end}}{{range .Sidebar}}{{if .Name}}<p class="side-group">{{.Name}}</p>
{{end}}<ul>
{{range .Pages}}<li><a href="{{.Href}}" data-page="{{.Page}}"{{if .Current}} aria-current="page"{{end}}>{{if .Lesson}}<span class="side-n">{{.Lesson}}</span> {{end}}{{.Title}}</a></li>
{{end}}</ul>
{{end}}</nav>
</div>
{{end}}<main class="doc-main" id="content" tabindex="-1">
<nav class="breadcrumb" aria-label="Breadcrumb"><ol>
{{range .Breadcrumb}}{{if .Current}}<li aria-current="page">{{.Title}}</li>{{else if .Href}}<li><a href="{{.Href}}">{{.Title}}</a></li>{{else}}<li class="crumb-group">{{.Title}}</li>{{end}}
{{end}}</ol></nav>
<article class="doc">
<header class="doc-head">
<p class="kicker">{{.Kicker}}</p>
<h1>{{.TitleHTML}}</h1>
{{if .Lede}}<p class="lede">{{.Lede}}</p>
{{end}}{{if .Meta}}<dl class="lesson-meta">
{{range .Meta}}<div{{if .Class}} class="{{.Class}}"{{end}}><dt>{{.Label}}</dt><dd>{{.Value}}</dd></div>
{{end}}</dl>
{{end}}{{if .TwinHref}}<p class="doc-actions"><a class="view-md" href="{{.TwinHref}}" type="text/markdown">View as Markdown</a> <button class="copy-page" type="button" data-md="{{.TwinHref}}">Copy page</button></p>
{{end}}</header>
{{if .Runbook}}{{.Runbook}}
{{end}}{{if .TOC}}<details class="toc-inline">
<summary>On this page</summary>
<nav aria-label="On this page">
<ul>
{{range .TOC}}<li class="toc-l{{.Level}}"><a href="#{{.ID}}">{{.Text}}</a></li>
{{end}}</ul>
</nav>
</details>
{{end}}{{.Content}}
<footer class="doc-foot">
{{if or .Prev .Next}}<nav class="pager" aria-label="Previous and next page">
{{if .Prev}}<a class="pager-prev" rel="prev" href="{{.Prev.Href}}"><span class="pager-label">Previous</span>{{if .Prev.Kicker}}<span class="pager-kicker">{{.Prev.Kicker}}</span>{{end}}<span class="pager-title">{{.Prev.Title}}</span></a>
{{end}}{{if .Next}}<a class="pager-next" rel="next" href="{{.Next.Href}}"><span class="pager-label">Next</span>{{if .Next.Kicker}}<span class="pager-kicker">{{.Next.Kicker}}</span>{{end}}<span class="pager-title">{{.Next.Title}}</span></a>
{{end}}</nav>
{{end}}{{if .MarkDone}}<button class="mark-done" type="button" data-page="{{.PagePath}}">Mark as done</button>
{{end}}{{if .Applies}}<p class="applies">{{.Applies}}</p>
{{end}}</footer>
</article>
</main>
{{if .TOC}}<div class="doc-toc">
<nav aria-label="On this page">
<p class="toc-title">On this page</p>
<ul>
{{range .TOC}}<li class="toc-l{{.Level}}"><a href="#{{.ID}}">{{.Text}}</a></li>
{{end}}</ul>
</nav>
</div>
{{end}}</div>
{{template "footer" .Footer}}</body>
</html>
{{end}}

{{define "landing"}}{{template "head" .Head}}<body class="page page-landing" data-root="">
{{template "header" .Header}}
<main class="landing-main" id="content" tabindex="-1">
{{.Content}}
</main>
{{template "footer" .Footer}}</body>
</html>
{{end}}

{{define "contents"}}{{template "head" .Head}}<body class="page page-doc" data-section="contents" data-root="{{.Root}}">
{{template "header" .Header}}
<div class="doc-shell no-sidebar no-toc">
<main class="doc-main" id="content" tabindex="-1">
<article class="doc">
<header class="doc-head">
<p class="kicker">§ All pages</p>
<h1>All pages</h1>
<p class="lede">Every page of the documentation, by section.</p>
</header>
{{range .Sections}}<section class="contents-section" id="{{.ID}}">
<h2>{{.Title}}</h2>
{{if .Blurb}}<p>{{.Blurb}}</p>
{{end}}{{range .Groups}}{{if .Name}}<h3>{{.Name}}</h3>
{{end}}<dl class="contents-list">
{{range .Pages}}<dt><a href="{{.Href}}">{{.Title}}</a></dt>
<dd>{{.Description}}</dd>
{{end}}</dl>
{{end}}</section>
{{end}}</article>
</main>
</div>
{{template "footer" .Footer}}</body>
</html>
{{end}}

{{define "notfound"}}{{template "head" .Head}}<body class="page page-doc page-notfound" data-section="notfound" data-root="{{.Root}}">
{{template "header" .Header}}
<div class="doc-shell no-sidebar no-toc">
<main class="doc-main" id="content" tabindex="-1">
<article class="doc">
<header class="doc-head">
<p class="kicker">§ 404</p>
<h1>Page not found</h1>
<p class="lede">The address does not exist or has moved.</p>
<p class="doc-actions"><button class="btn" type="button" data-search-open>Search the documentation</button></p>
<p class="notfound-path" data-notfound-path></p>
</header>
<dl class="contents-list">
{{range .Links}}<dt><a href="{{.Href}}">{{.Title}}</a></dt>
<dd>{{.Description}}</dd>
{{end}}</dl>
</article>
</main>
</div>
{{template "footer" .Footer}}</body>
</html>
{{end}}

{{define "redirect"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Moved · {{.SiteName}}</title>
<meta name="robots" content="noindex">
<link rel="canonical" href="{{.Canonical}}">
<meta http-equiv="refresh" content="0; url={{.Href}}">
<link rel="icon" type="image/svg+xml" href="{{.Root}}assets/logo.svg">
<link rel="stylesheet" href="{{.Root}}assets/style.css">
</head>
<body class="page page-redirect">
<p>This page moved to <a href="{{.Href}}">{{.Title}}</a>.</p>
</body>
</html>
{{end}}
`

var shell = template.Must(template.New("shell").Parse(shellTemplates))

// View types for the templates.

type headView struct {
	Title       string
	OGTitle     string
	OGType      string
	Description string
	SiteName    string
	Root        string
	Canonical   string
	TwinHref    string
	Preload     []string
	Widgets     bool
	OGImage     string
	NoIndex     bool
	ICO         bool // site/favicon.ico exists
	TouchIcon   bool // site/assets/apple-touch-icon.png exists
}

type navItem struct {
	Title    string
	Href     string
	Current  bool
	IsPage   bool // the link goes to the page that is open, not only to its section
	External bool
}

type headerView struct {
	BrandHref  string
	LogoSrc    string
	BrandName  string
	Nav        []navItem
	RepoHref   string
	RepoLabel  string
	StartHref  string
	StartLabel string
}

type footerView struct {
	Name     string
	Sections []navItem // the sections of the header nav, each linking to its overview
	Project  []navItem // the repository, when set, the list of pages and llms.txt
	Applies  string
	Note     string
}

type sideEntry struct {
	Title   string
	Href    string
	Page    string
	Lesson  string
	Current bool
}

type sideGroup struct {
	Name  string
	Pages []sideEntry
}

type crumb struct {
	Title   string
	Href    string
	Current bool
}

type metaItem struct {
	Label string
	Class string
	Value template.HTML
}

type pagerLink struct {
	Title       string
	Href        string
	Kicker      string // "§ A4" for a lesson
	Description string
}

type tocItem struct {
	Level int
	ID    string
	Text  string
}

type docView struct {
	Head       headView
	Header     headerView
	Footer     footerView
	Section    string
	Root       string
	Pinned     []navItem
	Sidebar    []sideGroup
	Breadcrumb []crumb
	Kicker     string
	TitleHTML  template.HTML
	Lede       template.HTML
	Meta       []metaItem
	TwinHref   string
	Content    template.HTML
	Runbook    template.HTML
	Prev       *pagerLink
	Next       *pagerLink
	MarkDone   bool
	PagePath   string
	Applies    string
	TOC        []tocItem
}

type landingView struct {
	Head    headView
	Header  headerView
	Footer  footerView
	Content template.HTML
}

type contentsPage struct {
	Title       string
	Href        string
	Description string
}

type contentsGroup struct {
	Name  string
	Pages []contentsPage
}

type contentsSection struct {
	ID     string
	Title  string
	Blurb  string
	Groups []contentsGroup
}

type contentsView struct {
	Head     headView
	Header   headerView
	Footer   footerView
	Root     string
	Sections []contentsSection
}

type notFoundView struct {
	Head   headView
	Header headerView
	Footer footerView
	Root   string
	Links  []contentsPage
}

type redirectView struct {
	Root      string
	SiteName  string
	Canonical string
	Href      string
	Title     string
}
