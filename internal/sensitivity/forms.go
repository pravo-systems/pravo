package sensitivity

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html"
	"net/url"
	"strings"
)

const (
	// MinSecret is the shortest secret that is looked for in other values. A shorter
	// one matches too much to say anything.
	MinSecret = 6
	// MinForm is the shortest encoded form that is looked for.
	MinForm = 8
	// MaxFormed bounds the secret that gets every encoding: one of many kilobytes (a key) is
	// looked for as it is, and in base64, which is how a key is embedded.
	MaxFormed = 4096
)

// xmlEntities writes a secret as it reads inside an XML document (a generated password has & and < in it).
var xmlEntities = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

// Forms lists the encodings of a secret that a specification is likely to put into another
// attribute, apart from the secret itself, which the caller looks for as it is: URL query and
// path escapes, JSON string escapes (with and without HTML escaping), hex in both cases, HTML and
// XML entities, and base64 (standard and URL) at each of the three alignments within a group.
// It is the one list that every surface uses to say that a text is built from a secret.
func Forms(x string) []string { return FormsAtLeast(x, MinForm) }

// FormsAtLeast is Forms with a different length below which a form is not looked for: the log of
// a provider masks secrets of four characters, and the forms of those with them.
func FormsAtLeast(x string, minForm int) []string {
	var out []string
	add := func(f string) {
		if f != x && len(f) >= minForm {
			out = append(out, f)
		}
	}
	if len(x) <= MaxFormed {
		add(url.QueryEscape(x))
		add(url.PathEscape(x))
		add(JSONInner(x, true))
		add(JSONInner(x, false))
		add(hex.EncodeToString([]byte(x)))
		add(strings.ToUpper(hex.EncodeToString([]byte(x))))
		add(html.EscapeString(x))
		add(xmlEntities.Replace(x))
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		out = append(out, Base64Forms(enc, []byte(x), MinSecret)...)
	}
	return out
}

// JSONInner is the secret as it reads inside a JSON string.
func JSONInner(x string, escapeHTML bool) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(escapeHTML)
	if enc.Encode(x) != nil {
		return x
	}
	out := strings.TrimSpace(b.String())
	if len(out) < 2 {
		return x
	}
	return out[1 : len(out)-1]
}

// Base64Forms returns what the bytes become when they are part of a longer text that is
// base64-encoded: the encoding depends on where in a group of three they start, and the first and
// last characters of the encoding depend on the bytes around them, so those are left out.
func Base64Forms(enc *base64.Encoding, secret []byte, minLen int) []string {
	var out []string
	for lead := range 3 {
		text := strings.TrimRight(enc.EncodeToString(append(make([]byte, lead), secret...)), "=")
		// The characters that the lead bytes take part in are not the secret's.
		drop := [3]int{0, 2, 3}[lead]
		if drop >= len(text) {
			continue
		}
		text = text[drop:]
		// A group that the secret ends inside has a last character that depends on what follows.
		if (lead+len(secret))%3 != 0 && len(text) > 0 {
			text = text[:len(text)-1]
		}
		if len(text) >= minLen {
			out = append(out, text)
		}
	}
	return out
}

// Taint holds the secrets of a state with every form they may take in another value, so that
// many texts can be judged against them without building the forms again.
type Taint struct{ texts []string }

// Add records a secret and its forms. A secret shorter than MinSecret is ignored, as is one of more than 64 KiB.
func (t *Taint) Add(x string) {
	if len(x) < MinSecret || len(x) > 1<<16 {
		return
	}
	t.texts = append(t.texts, x)
	t.texts = append(t.texts, Forms(x)...)
}

// Hits says whether text holds a secret of the taint, as it is or in one of its forms.
func (t *Taint) Hits(text string) bool {
	if len(text) < MinSecret {
		return false
	}
	for _, f := range t.texts {
		if strings.Contains(text, f) {
			return true
		}
	}
	return false
}

// Tainted says whether text holds one of the secrets, as it is or in any of its Forms.
func Tainted(text string, secrets []string) bool {
	var t Taint
	for _, x := range secrets {
		t.Add(x)
	}
	return t.Hits(text)
}
