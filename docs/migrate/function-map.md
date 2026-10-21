---
title: Function map
description: Every OpenTofu built-in function, with the CUE expression that replaces it, or the statement that none does.
order: 21
group: Annexes
minutes: 15
needs: "[The concept map](m2-concept-map.md) read."
learn: Which CUE builtin or expression does each OpenTofu function become?
done_when: "Each function in your specification has a row here, and its CUE expression evaluates."
level: intermediate
normalize:
  - pattern: "v[0-9]+\\.[0-9]+\\.[0-9]+"
    replace: "v<version>"
keywords: "templatefile cidrsubnet lookup merge function terraform tofu hcl"
---

OpenTofu 1.13 has 134 built-in functions, and `tofu metadata functions -json` lists them. CUE has a standard library of packages and a few builtins, and a comprehension replaces what neither covers. This page maps each function to its replacement, or says that there is none. `pravo eval -c` evaluates every CUE expression in the tables at the end of the page. The result column is that output.

Use a function by importing its package. An import that nothing uses is an error. The names after the imports are the ones the tables use.

| Package | Import | Holds |
|---|---|---|
| strings | `"strings"` | `ToUpper`, `Join`, `Split`, `Replace`, `TrimSpace`, `SliceRunes` |
| list | `"list"` | `Range`, `Slice`, `Concat`, `Contains`, `Sort`, `Max`, `Sum`, `MinItems`, `MaxItems` |
| struct | `"struct"` | `MinFields`, `MaxFields` |
| math | `"math"` | `Abs`, `Ceil`, `Floor`, `Log`, `Pow` |
| strconv | `"strconv"` | `Atoi`, `ParseInt`, `ParseFloat`, `ParseBool` |
| regexp | `"regexp"` | `Find`, `FindAll`, `ReplaceAll`, `Match` |
| net | `"net"` | `AddIP`, `ParseCIDR`, `InCIDR`, `QueryEscape`, `QueryUnescape`, `IPCIDR` |
| path, time, uuid | `"path"`, `"time"`, `"uuid"` | `Base`, `Dir`, `FormatString`, `SHA1` |
| encoding | `"encoding/base64"`, `"encoding/json"`, `"encoding/yaml"`, `"encoding/csv"`, `"encoding/hex"` | `Encode`, `Marshal`, `Unmarshal`, `Decode` |
| crypto | `"crypto/md5"`, `"crypto/sha1"`, `"crypto/sha256"`, `"crypto/sha512"` | `Sum`, `Sum256`, `Sum512` |
| text/template | `"text/template"` | `Execute` |

The CUE builtins need no import: `len`, `div`, `mod`, `quo`, `rem`, and the operators. A row that says `No equivalent` gives a replacement in the note when there is one.

## The functions

### Numeric

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `abs(-5)` | `math.Abs(-5)` | `5` |  |
| `ceil(1.2)` | `math.Ceil(1.2)` | `2` |  |
| `floor(1.8)` | `math.Floor(1.8)` | `1` |  |
| `log(8, 2)` | `math.Log(8) / math.Log(2)` | `3.0` | `math.Log` is the natural logarithm; the result is a float. |
| `max(1, 3, 2)` | `list.Max([1, 3, 2])` | `3` | `list.Max` takes one list. |
| `min(1, 3, 2)` | `list.Min([1, 3, 2])` | `1` | `list.Min` takes one list. |
| `parseint("ff", 16)` | `strconv.ParseInt("ff", 16, 64)` | `255` |  |
| `pow(2, 3)` | `math.Pow(2, 3)` | `8` |  |
| `signum(-5)` | `[if _n > 0 {1}, if _n < 0 {-1}, 0][0]` | `-1` | No builtin; the list picks the first branch that holds. Here `_n: -5`. |
:::

### String

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `chomp("hello\n")` | `strings.TrimRight("hello\n", "\r\n")` | `"hello"` |  |
| `endswith("hello", "llo")` | `strings.HasSuffix("hello", "llo")` | `true` |  |
| `format("%05d", 42)` | `template.Execute("{{printf \"%05d\" .n}}", {n: 42})` | `"00042"` | `text/template` uses Go verbs and Go template syntax. For `%s` and `%d` alone, use interpolation: `"\(a)-\(b)"`. |
| `formatlist("web-%s", ["a", "b"])` | `[for x in ["a", "b"] {"web-\(x)"}]` | `["web-a", "web-b"]` |  |
| `indent(2, "a\nb")` | `strings.Replace("a\nb", "\n", "\n  ", -1)` |  | Neither indents the first line. |
| `join(",", ["a", "b"])` | `strings.Join(["a", "b"], ",")` | `"a,b"` |  |
| `lower("AbC")` | `strings.ToLower("AbC")` | `"abc"` |  |
| `regex("[a-z]+", "123abc456")` | `regexp.Find("[a-z]+", "123abc456")` | `"abc"` | `regexp.FindSubmatch` returns the groups. |
| `regexall("[0-9]+", "a1b22c333")` | `regexp.FindAll("[0-9]+", "a1b22c333", -1)` | `["1", "22", "333"]` |  |
| `replace("a-b-c", "-", "_")` | `strings.Replace("a-b-c", "-", "_", -1)` | `"a_b_c"` | `-1` replaces every match. |
| `replace("a1b22", "/[0-9]+/", "#")` | `regexp.ReplaceAll("[0-9]+", "a1b22", "#")` | `"a#b#"` | The HCL form is `replace` with a pattern between slashes. |
| `split(",", "a,b,c")` | `strings.Split("a,b,c", ",")` | `["a", "b", "c"]` | The string comes first in CUE. |
| `startswith("hello", "he")` | `strings.HasPrefix("hello", "he")` | `true` |  |
| `strcontains("hello", "ell")` | `strings.Contains("hello", "ell")` | `true` |  |
| `strrev("abc")` | `strings.Join(list.Reverse(strings.Split("abc", "")), "")` | `"cba"` | No builtin; the string is split into characters. |
| `substr("hello", 1, 2)` | `strings.SliceRunes("hello", 1, 3)` | `"el"` | The end of `SliceRunes` is exclusive. |
| `templatestring("Hello, $${name}!", {name = "pravo"})` | `template.Execute("Hello, {{.name}}!", {name: "pravo"})` | `"Hello, pravo!"` | `templatestring` renders a template that a value holds. The placeholders of `text/template` are `{{.name}}`. |
| `title("hello world")` | `strings.ToTitle("hello world")` | `"Hello World"` |  |
| `trim("xxhixx", "x")` | `strings.Trim("xxhixx", "x")` | `"hi"` |  |
| `trimprefix("hello", "he")` | `strings.TrimPrefix("hello", "he")` | `"llo"` |  |
| `trimsuffix("hello", "lo")` | `strings.TrimSuffix("hello", "lo")` | `"hel"` |  |
| `trimspace("  hi ")` | `strings.TrimSpace("  hi ")` | `"hi"` |  |
| `upper("abc")` | `strings.ToUpper("abc")` | `"ABC"` |  |
:::

### Collection

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `alltrue([true, false])` | `!list.Contains([true, false], false)` | `false` |  |
| `anytrue([false, true])` | `list.Contains([false, true], true)` | `true` |  |
| `chunklist([1, 2, 3, 4, 5], 2)` | `[for i in list.Range(0, 5, 2) {list.Slice([1, 2, 3, 4, 5], i, [if i+2 < 5 {i+2}, 5][0])}]` | `[[1, 2], [3, 4], [5]]` | No builtin. |
| `coalesce(null, "", "b", "c")` | `[for x in [null, "", "b", "c"] if x != null && x != "" {x}][0]` | `"b"` |  |
| `coalescelist([], ["x"], ["y"])` | `[for l in [[], ["x"], ["y"]] if len(l) > 0 {l}][0]` | `["x"]` |  |
| `compact(["a", "", "b"])` | `[for x in ["a", "", "b"] if x != "" {x}]` | `["a", "b"]` |  |
| `concat([1, 2], [3])` | `list.Concat([[1, 2], [3]])` | `[1, 2, 3]` |  |
| `contains(["a", "b"], "b")` | `list.Contains(["a", "b"], "b")` | `true` |  |
| `distinct([1, 2, 1])` | `[for i, x in [1, 2, 1] if !list.Contains(list.Slice([1, 2, 1], 0, i), x) {x}]` | `[1, 2]` | No builtin. `list.UniqueItems` checks that a list has no duplicates and does not remove them. |
| `element(["a", "b", "c"], 4)` | `_l[mod(4, len(_l))]` | `"b"` | Both wrap around. Here `_l: ["a", "b", "c"]`. |
| `flatten([[1], [2, [3]]])` | `list.FlattenN([[1], [2, [3]]], -1)` | `[1, 2, 3]` |  |
| `index(["a", "b", "c"], "b")` | `[for i, x in _l if x == "b" {i}][0]` | `1` | Fails when the value is not in the list. |
| `keys({a = 1, b = 2})` | `[for k, _ in _m {k}]` | `["a", "b"]` | Keys come in the order written. OpenTofu sorts them. Wrap with `list.SortStrings` to sort. Here `_m: {a: 1, b: 2}`. |
| `length(["a", "b", "c"])` | `len(_l)` | `3` | For a string `len` counts bytes. Count characters with `len(strings.Split(s, ""))`. |
| `lookup({a = 1}, "z", 0)` | `*_m["z"] \| 0` | `0` | The default marker goes on the lookup. With `_m: {a: 1}` the result is 0, and for a present key the value. |
| `matchkeys([1, 2, 3], ["a", "b", "c"], ["a", "c"])` | `[for i, k in ["a", "b", "c"] if list.Contains(["a", "c"], k) {[1, 2, 3][i]}]` | `[1, 3]` |  |
| `merge({a = 1, b = 2}, {c = 3})` | `_m & {c: 3}` |  | Unification, not an override: two values for one key are a conflict. See the note below. |
| `one([5])` | `[if len([5]) == 1 {[5][0]}, null][0]` | `5` |  |
| `range(0, 5, 2)` | `list.Range(0, 5, 2)` | `[0, 2, 4]` | `range(3)` is `list.Range(0, 3, 1)`. |
| `reverse(["a", "b", "c"])` | `list.Reverse(_l)` | `["c", "b", "a"]` |  |
| `setintersection([1, 2, 3], [2, 3, 4])` | `[for x in [1, 2, 3] if list.Contains([2, 3, 4], x) {x}]` | `[2, 3]` |  |
| `setproduct([1, 2], ["x", "y"])` | `[for a in [1, 2] for b in ["x", "y"] {[a, b]}]` | `[[1, "x"], [1, "y"], [2, "x"], [2, "y"]]` |  |
| `setsubtract([1, 2, 3], [2])` | `[for x in [1, 2, 3] if !list.Contains([2], x) {x}]` | `[1, 3]` |  |
| `setunion([1, 2], [2, 3])` | `[for i, x in list.Concat([[1, 2], [2, 3]]) if !list.Contains(list.Slice(list.Concat([[1, 2], [2, 3]]), 0, i), x) {x}]` | `[1, 2, 3]` | A concatenation without duplicates. |
| `slice(["a", "b", "c"], 1, 3)` | `list.Slice(_l, 1, 3)` | `["b", "c"]` |  |
| `sort(["b", "a", "c"])` | `list.SortStrings(["b", "a", "c"])` | `["a", "b", "c"]` | `list.Sort(l, list.Ascending)` sorts numbers. |
| `sum([1, 2, 3])` | `list.Sum([1, 2, 3])` | `6` |  |
| `transpose({a = ["1", "2"], b = ["2"]})` | `{for k, v in {a: ["1", "2"], b: ["2"]} for x in v {"\(x)": [for k2, v2 in {a: ["1", "2"], b: ["2"]} if list.Contains(v2, x) {k2}]}}` |  | The result is a map from each value to the keys that hold it. |
| `values({a = 1, b = 2})` | `[for _, v in _m {v}]` | `[1, 2]` | In the order written. |
| `zipmap(["a", "b"], [10, 20])` | `{for i, k in ["a", "b"] {"\(k)": [10, 20][i]}}` |  |  |
:::

### Encoding

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `base64encode("hello")` | `base64.Encode(null, "hello")` | `"aGVsbG8="` |  |
| `base64decode("aGVsbG8=")` | `"\(base64.Decode(null, "aGVsbG8="))"` | `"hello"` | `Decode` returns bytes; interpolation makes a string. |
| `base64gunzip(x)` | No equivalent |  | Decode the value outside and pass it as a tag. |
| `base64gzip("hello")` | No equivalent |  | Compute the value outside and pass it as a tag. |
| `csvdecode("a,b\n1,2\n")` | `csv.Decode("a,b\n1,2\n")` | `[["a", "b"], ["1", "2"]]` | The result is a list of rows. Build maps from the header row with a comprehension. |
| `jsondecode("{\"a\": 1}")` | `json.Unmarshal("{\"a\": 1}")` | `a: 1` |  |
| `jsonencode({a = 1, b = [true]})` | `json.Marshal({a: 1, b: [true]})` | `"{\"a\":1,\"b\":[true]}"` | Keys come in the order written. OpenTofu sorts them. |
| `textdecodebase64("aGk=", "UTF-8")` | No equivalent |  | Only UTF-8 is read: use `base64decode`. |
| `textencodebase64("hi", "UTF-8")` | `base64.Encode(null, "hi")` | `"aGk="` | Only UTF-8 is written. |
| `urldecode("a+b%26c")` | `net.QueryUnescape("a+b%26c")` | `"a b&c"` |  |
| `urlencode("a b&c")` | `net.QueryEscape("a b&c")` | `"a+b%26c"` |  |
| `yamldecode("a: 1\nb: [x]")` | `yaml.Unmarshal("a: 1\nb: [x]")` |  |  |
| `yamlencode({a = 1, b = ["x"]})` | `yaml.Marshal({a: 1, b: ["x"]})` |  | The text can differ in layout from the OpenTofu output. |
:::

### Filesystem

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `abspath("x")` | No equivalent |  | A specification is pure. Pass the path as a tag, or use `-T` for `cwd`. |
| `basename("/a/b/c.txt")` | `path.Base("/a/b/c.txt")` | `"c.txt"` |  |
| `dirname("/a/b/c.txt")` | `path.Dir("/a/b/c.txt")` | `"/a/b"` |  |
| `pathexpand("~/x")` | No equivalent |  | Pass the expanded path as a tag. |
| `file("hello.txt")` | An embedded file |  | `@embed(file=hello.txt, type=text)`. See below. |
| `fileexists("hello.txt")` | No equivalent |  | A file that is not there leaves its embedded value incomplete, and `pravo eval -c` fails. |
| `fileset(".", "*.txt")` | An embedded file |  | `@embed(glob=*.txt, type=text)` gives a struct of file names to content. |
| `filebase64("hello.txt")` | An embedded file |  | `base64.Encode(null, _file)` of an embedded file. |
| `templatefile("x.tpl", {n = 1})` | An embedded file |  | `template.Execute(_tpl, {n: 1})` with Go template syntax. |
:::

### Date and time

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `formatdate("YYYY-MM-DD", "2023-11-14T22:13:20Z")` | `time.FormatString("2006-01-02", "2023-11-14T22:13:20Z")` | `"2023-11-14"` | The layout is the Go reference time, not `YYYY-MM-DD`. |
| `plantimestamp()` | No equivalent |  | Pass the time as a tag: `now: string @tag(now)`. |
| `timeadd("2023-11-14T22:13:20Z", "1h")` | No equivalent |  | Pass the result as a tag: `until: string @tag(until)`. |
| `timecmp("2023-11-14T22:13:20Z", "2023-11-15T00:00:00Z")` | `"2023-11-14T22:13:20Z" < "2023-11-15T00:00:00Z"` | `true` | For two UTC timestamps in the same layout. The result is a boolean, not -1, 0 or 1. |
| `timestamp()` | No equivalent |  | Pass the time as a tag: `now: string @tag(now)`. |
:::

### Hash and crypto

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `base64sha256("hello")` | `base64.Encode(null, sha256.Sum256("hello"))` | `"LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ="` |  |
| `base64sha512("hello")` | `base64.Encode(null, sha512.Sum512("hello"))` |  |  |
| `bcrypt("secret")` | No equivalent |  | The `bcrypt_hash` attribute of `random_password` holds a hash. |
| `filebase64sha256("hello.txt")` | An embedded file |  | `base64.Encode(null, sha256.Sum256(_file))`. |
| `filebase64sha512("hello.txt")` | An embedded file |  | `base64.Encode(null, sha512.Sum512(_file))`. |
| `filemd5("hello.txt")` | An embedded file |  | `hex.Encode(md5.Sum(_file))`. |
| `filesha1("hello.txt")` | An embedded file |  | `hex.Encode(sha1.Sum(_file))`. |
| `filesha256("hello.txt")` | An embedded file |  | `hex.Encode(sha256.Sum256(_file))`. |
| `filesha512("hello.txt")` | An embedded file |  | `hex.Encode(sha512.Sum512(_file))`. |
| `md5("hello")` | `hex.Encode(md5.Sum("hello"))` | `"5d41402abc4b2a76b9719d911017c592"` |  |
| `rsadecrypt(x, key)` | No equivalent |  |  |
| `sha1("hello")` | `hex.Encode(sha1.Sum("hello"))` | `"aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"` |  |
| `sha256("hello")` | `hex.Encode(sha256.Sum256("hello"))` |  |  |
| `sha512("hello")` | `hex.Encode(sha512.Sum512("hello"))` |  |  |
| `uuid()` | No equivalent |  | Evaluation is pure. The `random_uuid` resource of `hashicorp/random` makes a UUID once and records it. |
| `uuidv5("dns", "example.com")` | `uuid.SHA1("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "example.com")` | `"cfbff0d1-9375-5685-968c-48ce8b15ae17"` | The first argument is the namespace UUID. This one is the DNS namespace. |
:::

### IP network

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `cidrcontains("10.0.0.0/16", "10.0.1.5")` | `net.InCIDR("10.0.1.5", "10.0.0.0/16")` | `true` | The arguments change places. `InCIDR` takes an address and not a prefix. |
| `cidrhost("10.0.0.0/24", 5)` | `net.AddIP(strings.Split("10.0.0.0/24", "/")[0], 5)` | `"10.0.0.5"` | Valid when the prefix is the network address. |
| `cidrnetmask("10.0.0.0/24")` | `net.ParseCIDR("10.0.0.0/24").prefix_mask` | `"255.255.255.0"` | `ParseCIDR` also gives `prefix_len`, `prefix_addr` and `broadcast_addr`. |
| `cidrsubnet("10.0.0.0/16", 8, 2)` | `net.AddIP(strings.Split("10.0.0.0/16", "/")[0], 2 * (math.Pow(2, 32-(strconv.Atoi(strings.Split("10.0.0.0/16", "/")[1])+8)) & int)) + "/\(strconv.Atoi(strings.Split("10.0.0.0/16", "/")[1])+8)"` | `"10.0.2.0/24"` | IPv4. Write it once as a definition when you use it often. |
| `cidrsubnets("10.0.0.0/16", 4, 4)` | No equivalent |  | Write a comprehension over the `cidrsubnet` expression. |
:::

### Assertions

An `assume` function returns its value when the value meets a condition and fails otherwise. A constraint joined to the value does the same.

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `assumeequal(1, 1)` | `1 & 1` | `1` | Two values that differ are a conflict. |
| `assumenotnull("x")` | `"x" & !=null` | `"x"` |  |
| `assumestringprefix("pre-x", "pre-")` | `"pre-x" & =~"^pre-"` | `"pre-x"` |  |
| `assumelistlength([1, 2], 1, 3)` | `[1, 2] & list.MinItems(1) & list.MaxItems(3)` | `[1, 2]` |  |
| `assumelistlengthmax([1, 2], 3)` | `[1, 2] & list.MaxItems(3)` | `[1, 2]` |  |
| `assumelistlengthmin([1, 2], 1)` | `[1, 2] & list.MinItems(1)` | `[1, 2]` |  |
| `assumemaplength({a = 1}, 1, 3)` | `{a: 1} & struct.MinFields(1) & struct.MaxFields(3)` | `a: 1` |  |
| `assumemaplengthmax({a = 1}, 3)` | `{a: 1} & struct.MaxFields(3)` | `a: 1` |  |
| `assumemaplengthmin({a = 1}, 1)` | `{a: 1} & struct.MinFields(1)` | `a: 1` |  |
| `assumesetlength(toset([1, 2]), 1, 3)` | `[1, 2] & list.MinItems(1) & list.MaxItems(3)` | `[1, 2]` | CUE has lists and no sets. |
| `assumesetlengthmax(toset([1, 2]), 3)` | `[1, 2] & list.MaxItems(3)` | `[1, 2]` |  |
| `assumesetlengthmin(toset([1, 2]), 1)` | `[1, 2] & list.MinItems(1)` | `[1, 2]` |  |
:::

### Type conversion

:::mapping
| OpenTofu | CUE | Result | Note |
|---|---|---|---|
| `can(local.x)` | `(_m["z"] != _\|_)` | `false` | Tests whether a reference or an expression is defined. |
| `convert("5", number)` | `strconv.Atoi("5")` | `5` | `strconv.ParseFloat` parses a float, `strconv.ParseBool` a boolean, and `"\(x)"` makes a string. |
| `ephemeralasnull(x)` | No equivalent |  | pravo never records an ephemeral value. See [Secrets and ephemeral values](../concepts/secrets-and-ephemeral-values.md). |
| `issensitive(x)` | No equivalent |  | Sensitivity belongs to the provider schema and to `sensitive: true` on an output. |
| `nonsensitive(x)` | No equivalent |  | Sensitivity belongs to the provider schema and to `sensitive: true` on an output. |
| `sensitive(x)` | No equivalent |  | Mark an output `sensitive: true`. |
| `tobool("true")` | `strconv.ParseBool("true")` | `true` |  |
| `tolist(x)` | No equivalent |  | Not needed. CUE has one list type. |
| `tomap(x)` | No equivalent |  | Not needed. CUE has one struct type. |
| `tonumber("3.5")` | `strconv.ParseFloat("3.5", 64)` | `3.5` | `strconv.Atoi` parses an integer. |
| `toset([1, 2, 1])` | `[for i, x in [1, 2, 1] if !list.Contains(list.Slice([1, 2, 1], 0, i), x) {x}]` | `[1, 2]` | Only needed to remove duplicates. A loop over a list needs no set. |
| `tostring(5)` | `"\(5)"` | `"5"` |  |
| `try(local.m.z, "d")` | `*_m["z"] \| "d"` | `"d"` | The default marker goes on the first expression. |
| `type(x)` | No equivalent |  | Test a value against a type: `(x & string) != _\|_`. OpenTofu has `type` in `tofu console` only. |
:::


## Differences that change a result

Five differences matter when a result feeds a resource.

- **Order of keys.** `keys`, `values` and `jsonencode` sort the keys in OpenTofu. A CUE struct keeps the order you wrote. Sort with `list.SortStrings` when the order must not depend on the source.
- **Conflicts.** `merge` lets the last map win. `&` unifies, and two different values for one key are an error. Mark the value that may be replaced with `*`: `{encrypted: *true | bool}`. [Pitfalls](m9-pitfalls.md#merging-with-an-override) shows the error and the fix.
- **Defaults.** `lookup(m, k, d)` and `try(a, b)` are `*m[k] | d` and `*a | b`. The marker goes on the expression that may fail. The form `m[k] | *d` returns `d` even when the key exists.
- **Length.** `len` counts bytes. `length("héllo")` is 5 in OpenTofu. `len("héllo")` is 6.
- **No clock, no randomness.** There is no `timestamp()` and no `uuid()`. A tag carries a time. A resource of `hashicorp/random` carries a random value, recorded once.

## Provider-defined functions

`provider::kubernetes::manifest_decode(yaml)` is a call in OpenTofu. In pravo a call is a declaration, built through the provider like a data source. The arguments go in `args`, and the answer is `result`. The function is pure, its result is never recorded, and it needs no cluster.

:::compare
```hcl title="main.tf"
locals {
  manifest = provider::kubernetes::manifest_decode(file("cm.yaml"))
}

output "kind" {
  value = local.manifest.kind
}
```

```cue file=calls/main.cue title="calls/main.cue"
package calls

import (
	"pravo.dev/pravo"
	k8s "pravo.dev/providers/hashicorp/kubernetes"
)

$k8s: k8s.#Provider

manifest: $k8s.#Function.manifest_decode & {
	args: ["apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n"]
}

kind: pravo.#Output & {value: manifest.result.kind}
```
:::

```sh run
pravo -C calls init > /dev/null
pravo -C calls plan 2>&1 | tail -n 4
```

```text out
Graph: planned 0 of 1, 1 functions called.

Outputs:
  kind: "ConfigMap"
```

In a root that declares the provider, `pravo schema kubernetes/manifest_decode` prints the parameters and the documentation of the function.

## Files

A function that reads a file becomes an embedded file. The directory must be a CUE module, because `@embed` reads files relative to the module. The first line of the file is `@extern(embed)`. The file name stays a plain string: `@embed(file=hello.txt, type=text)`, and `@embed(glob=*.txt, type=text)` embeds a set.

```text file=files/hello.txt title="files/hello.txt"
hello
```

```text file=files/greeting.tpl title="files/greeting.tpl"
Hello, {{.name}}! n={{.n}}
```

```cue file=files/cue.mod/module.cue title="files/cue.mod/module.cue"
module: "example.com/files@v0"
language: version: "v0.14.0"
```

```cue file=files/main.cue title="files/main.cue"
@extern(embed)
package files

import (
	"encoding/base64"
	"encoding/hex"
	"crypto/md5"
	"crypto/sha256"
	"text/template"
)

_file: _ @embed(file=hello.txt, type=text)
_tpl:  _ @embed(file=greeting.tpl, type=text)
_all:  _ @embed(glob=*.txt, type=text)

file:         _file
fileset:      [for name, _ in _all {name}]
filebase64:   base64.Encode(null, _file)
filemd5:      hex.Encode(md5.Sum(_file))
filesha256:   hex.Encode(sha256.Sum256(_file))
templatefile: template.Execute(_tpl, {name: "pravo", n: 3})
```

```sh run
pravo -C files eval -c
```

```text out
file: """
	hello

	"""
fileset: ["hello.txt"]
filebase64: "aGVsbG8K"
filemd5:    "b1946ac92492d2347c6235b4d2611184"
filesha256: "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
templatefile: """
	Hello, pravo! n=3

	"""
```

The digests equal the MD5 and SHA-256 digests of the file. The template uses Go template syntax. A template written for `templatefile` with `${name}` needs `{{.name}}`, and a `%{ for }` directive needs `{{range}}`.

:::deep-dive Evaluate every row
The file below holds each CUE expression of the tables as a field, with the helper values `_n`, `_l` and `_m`. `pravo eval -c` fails when a field is not concrete. A row that stops evaluating fails the page.

```cue file=functions.cue title="functions.cue"
package fn

import (
	"math"
	"strings"
	"strconv"
	"list"
	"struct"
	"regexp"
	"net"
	"path"
	"time"
	"uuid"
	"text/template"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/yaml"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
)

_n: -5
_l: ["a", "b", "c"]
_m: {a: 1, b: 2}

fn_abs: math.Abs(-5)
fn_ceil: math.Ceil(1.2)
fn_floor: math.Floor(1.8)
fn_log: math.Log(8) / math.Log(2)
fn_max: list.Max([1, 3, 2])
fn_min: list.Min([1, 3, 2])
fn_parseint: strconv.ParseInt("ff", 16, 64)
fn_pow: math.Pow(2, 3)
fn_signum: [if _n > 0 {1}, if _n < 0 {-1}, 0][0]
fn_chomp: strings.TrimRight("hello\n", "\r\n")
fn_endswith: strings.HasSuffix("hello", "llo")
fn_format: template.Execute("{{printf \"%05d\" .n}}", {n: 42})
fn_formatlist: [for x in ["a", "b"] {"web-\(x)"}]
fn_indent: strings.Replace("a\nb", "\n", "\n  ", -1)
fn_join: strings.Join(["a", "b"], ",")
fn_lower: strings.ToLower("AbC")
fn_regex: regexp.Find("[a-z]+", "123abc456")
fn_regexall: regexp.FindAll("[0-9]+", "a1b22c333", -1)
fn_replace: strings.Replace("a-b-c", "-", "_", -1)
fn_replace_regex: regexp.ReplaceAll("[0-9]+", "a1b22", "#")
fn_split: strings.Split("a,b,c", ",")
fn_startswith: strings.HasPrefix("hello", "he")
fn_strcontains: strings.Contains("hello", "ell")
fn_strrev: strings.Join(list.Reverse(strings.Split("abc", "")), "")
fn_substr: strings.SliceRunes("hello", 1, 3)
fn_templatestring: template.Execute("Hello, {{.name}}!", {name: "pravo"})
fn_title: strings.ToTitle("hello world")
fn_trim: strings.Trim("xxhixx", "x")
fn_trimprefix: strings.TrimPrefix("hello", "he")
fn_trimsuffix: strings.TrimSuffix("hello", "lo")
fn_trimspace: strings.TrimSpace("  hi ")
fn_upper: strings.ToUpper("abc")
fn_alltrue: !list.Contains([true, false], false)
fn_anytrue: list.Contains([false, true], true)
fn_chunklist: [for i in list.Range(0, 5, 2) {list.Slice([1, 2, 3, 4, 5], i, [if i+2 < 5 {i+2}, 5][0])}]
fn_coalesce: [for x in [null, "", "b", "c"] if x != null && x != "" {x}][0]
fn_coalescelist: [for l in [[], ["x"], ["y"]] if len(l) > 0 {l}][0]
fn_compact: [for x in ["a", "", "b"] if x != "" {x}]
fn_concat: list.Concat([[1, 2], [3]])
fn_contains: list.Contains(["a", "b"], "b")
fn_distinct: [for i, x in [1, 2, 1] if !list.Contains(list.Slice([1, 2, 1], 0, i), x) {x}]
fn_element: _l[mod(4, len(_l))]
fn_flatten: list.FlattenN([[1], [2, [3]]], -1)
fn_index: [for i, x in _l if x == "b" {i}][0]
fn_keys: [for k, _ in _m {k}]
fn_length: len(_l)
fn_lookup: *_m["z"] | 0
fn_matchkeys: [for i, k in ["a", "b", "c"] if list.Contains(["a", "c"], k) {[1, 2, 3][i]}]
fn_merge: _m & {c: 3}
fn_one: [if len([5]) == 1 {[5][0]}, null][0]
fn_range: list.Range(0, 5, 2)
fn_reverse: list.Reverse(_l)
fn_setintersection: [for x in [1, 2, 3] if list.Contains([2, 3, 4], x) {x}]
fn_setproduct: [for a in [1, 2] for b in ["x", "y"] {[a, b]}]
fn_setsubtract: [for x in [1, 2, 3] if !list.Contains([2], x) {x}]
fn_setunion: [for i, x in list.Concat([[1, 2], [2, 3]]) if !list.Contains(list.Slice(list.Concat([[1, 2], [2, 3]]), 0, i), x) {x}]
fn_slice: list.Slice(_l, 1, 3)
fn_sort: list.SortStrings(["b", "a", "c"])
fn_sum: list.Sum([1, 2, 3])
fn_transpose: {for k, v in {a: ["1", "2"], b: ["2"]} for x in v {"\(x)": [for k2, v2 in {a: ["1", "2"], b: ["2"]} if list.Contains(v2, x) {k2}]}}
fn_values: [for _, v in _m {v}]
fn_zipmap: {for i, k in ["a", "b"] {"\(k)": [10, 20][i]}}
fn_base64encode: base64.Encode(null, "hello")
fn_base64decode: "\(base64.Decode(null, "aGVsbG8="))"
fn_csvdecode: csv.Decode("a,b\n1,2\n")
fn_jsondecode: json.Unmarshal("{\"a\": 1}")
fn_jsonencode: json.Marshal({a: 1, b: [true]})
fn_textencodebase64: base64.Encode(null, "hi")
fn_urldecode: net.QueryUnescape("a+b%26c")
fn_urlencode: net.QueryEscape("a b&c")
fn_yamldecode: yaml.Unmarshal("a: 1\nb: [x]")
fn_yamlencode: yaml.Marshal({a: 1, b: ["x"]})
fn_basename: path.Base("/a/b/c.txt")
fn_dirname: path.Dir("/a/b/c.txt")
fn_formatdate: time.FormatString("2006-01-02", "2023-11-14T22:13:20Z")
fn_timecmp: "2023-11-14T22:13:20Z" < "2023-11-15T00:00:00Z"
fn_base64sha256: base64.Encode(null, sha256.Sum256("hello"))
fn_base64sha512: base64.Encode(null, sha512.Sum512("hello"))
fn_md5: hex.Encode(md5.Sum("hello"))
fn_sha1: hex.Encode(sha1.Sum("hello"))
fn_sha256: hex.Encode(sha256.Sum256("hello"))
fn_sha512: hex.Encode(sha512.Sum512("hello"))
fn_uuidv5: uuid.SHA1("6ba7b810-9dad-11d1-80b4-00c04fd430c8", "example.com")
fn_cidrcontains: net.InCIDR("10.0.1.5", "10.0.0.0/16")
fn_cidrhost: net.AddIP(strings.Split("10.0.0.0/24", "/")[0], 5)
fn_cidrnetmask: net.ParseCIDR("10.0.0.0/24").prefix_mask
fn_cidrsubnet: net.AddIP(strings.Split("10.0.0.0/16", "/")[0], 2 * (math.Pow(2, 32-(strconv.Atoi(strings.Split("10.0.0.0/16", "/")[1])+8)) & int)) + "/\(strconv.Atoi(strings.Split("10.0.0.0/16", "/")[1])+8)"
fn_assumeequal: 1 & 1
fn_assumenotnull: "x" & !=null
fn_assumestringprefix: "pre-x" & =~"^pre-"
fn_assumelistlength: [1, 2] & list.MinItems(1) & list.MaxItems(3)
fn_assumelistlengthmax: [1, 2] & list.MaxItems(3)
fn_assumelistlengthmin: [1, 2] & list.MinItems(1)
fn_assumemaplength: {a: 1} & struct.MinFields(1) & struct.MaxFields(3)
fn_assumemaplengthmax: {a: 1} & struct.MaxFields(3)
fn_assumemaplengthmin: {a: 1} & struct.MinFields(1)
fn_assumesetlength: [1, 2] & list.MinItems(1) & list.MaxItems(3)
fn_assumesetlengthmax: [1, 2] & list.MaxItems(3)
fn_assumesetlengthmin: [1, 2] & list.MinItems(1)
fn_can: (_m["z"] != _|_)
fn_convert: strconv.Atoi("5")
fn_tobool: strconv.ParseBool("true")
fn_tonumber: strconv.ParseFloat("3.5", 64)
fn_toset: [for i, x in [1, 2, 1] if !list.Contains(list.Slice([1, 2, 1], 0, i), x) {x}]
fn_tostring: "\(5)"
fn_try: *_m["z"] | "d"
```

```sh run
pravo eval -c
```

```text out
fn_abs:      5
fn_ceil:     2
fn_floor:    1
fn_log:      3.0
fn_max:      3
fn_min:      1
fn_parseint: 255
fn_pow:      8
fn_signum:   -1
fn_chomp:    "hello"
fn_endswith: true
fn_format:   "00042"
fn_formatlist: ["web-a", "web-b"]
fn_indent: """
	a
	  b
	"""
fn_join:  "a,b"
fn_lower: "abc"
fn_regex: "abc"
fn_regexall: ["1", "22", "333"]
fn_replace:       "a_b_c"
fn_replace_regex: "a#b#"
fn_split: ["a", "b", "c"]
fn_startswith:     true
fn_strcontains:    true
fn_strrev:         "cba"
fn_substr:         "el"
fn_templatestring: "Hello, pravo!"
fn_title:          "Hello World"
fn_trim:           "hi"
fn_trimprefix:     "llo"
fn_trimsuffix:     "hel"
fn_trimspace:      "hi"
fn_upper:          "ABC"
fn_alltrue:        false
fn_anytrue:        true
fn_chunklist: [[1, 2], [3, 4], [5]]
fn_coalesce: "b"
fn_coalescelist: ["x"]
fn_compact: ["a", "b"]
fn_concat: [1, 2, 3]
fn_contains: true
fn_distinct: [1, 2]
fn_element: "b"
fn_flatten: [1, 2, 3]
fn_index: 1
fn_keys: ["a", "b"]
fn_length: 3
fn_lookup: 0
fn_matchkeys: [1, 3]
fn_merge: {
	a: 1
	b: 2
	c: 3
}
fn_one: 5
fn_range: [0, 2, 4]
fn_reverse: ["c", "b", "a"]
fn_setintersection: [2, 3]
fn_setproduct: [[1, "x"], [1, "y"], [2, "x"], [2, "y"]]
fn_setsubtract: [1, 3]
fn_setunion: [1, 2, 3]
fn_slice: ["b", "c"]
fn_sort: ["a", "b", "c"]
fn_sum: 6
fn_transpose: {
	"1": ["a"]
	"2": ["a", "b"]
}
fn_values: [1, 2]
fn_zipmap: {
	a: 10
	b: 20
}
fn_base64encode: "aGVsbG8="
fn_base64decode: "hello"
fn_csvdecode: [["a", "b"], ["1", "2"]]
fn_jsondecode: {
	a: 1
}
fn_jsonencode:       "{\"a\":1,\"b\":[true]}"
fn_textencodebase64: "aGk="
fn_urldecode:        "a b&c"
fn_urlencode:        "a+b%26c"
fn_yamldecode: {
	a: 1
	b: ["x"]
}
fn_yamlencode: """
	a: 1
	b:
	  - x

	"""
fn_basename:           "c.txt"
fn_dirname:            "/a/b"
fn_formatdate:         "2023-11-14"
fn_timecmp:            true
fn_base64sha256:       "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ="
fn_base64sha512:       "m3HSJL1i83hdltRq0+o9czGb+8KJDKra4t/3JRlnPKcjI8PZm6XBHXx6zG4UuMXaDEZjR1wuXDre9G9zvN7AQw=="
fn_md5:                "5d41402abc4b2a76b9719d911017c592"
fn_sha1:               "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
fn_sha256:             "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
fn_sha512:             "9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043"
fn_uuidv5:             "cfbff0d1-9375-5685-968c-48ce8b15ae17"
fn_cidrcontains:       true
fn_cidrhost:           "10.0.0.5"
fn_cidrnetmask:        "255.255.255.0"
fn_cidrsubnet:         "10.0.2.0/24"
fn_assumeequal:        1
fn_assumenotnull:      "x"
fn_assumestringprefix: "pre-x"
fn_assumelistlength: [1, 2]
fn_assumelistlengthmax: [1, 2]
fn_assumelistlengthmin: [1, 2]
fn_assumemaplength: {
	a: 1
}
fn_assumemaplengthmax: {
	a: 1
}
fn_assumemaplengthmin: {
	a: 1
}
fn_assumesetlength: [1, 2]
fn_assumesetlengthmax: [1, 2]
fn_assumesetlengthmin: [1, 2]
fn_can:      false
fn_convert:  5
fn_tobool:   true
fn_tonumber: 3.5
fn_toset: [1, 2]
fn_tostring: "5"
fn_try:      "d"
```
:::

## Recap

- Import the package, call the function, and check the result with `pravo eval -e`.
- A function with no builtin is a comprehension, a resource of a provider, or a tag that carries the value.
- Order, conflicts and defaults differ from OpenTofu: read the five differences before you replace `merge`, `lookup` and `try`.

:::check
How do you write `lookup(var.m, "k", "d")`?
::answer
`*m.k | "d"`, with the default marker on the lookup. The form `m.k | *"d"` returns the default even when `k` exists.

What replaces `timestamp()`?
::answer
A tag such as `now: string @tag(now)` carries a time that the caller supplies.
:::

Next: [Command map](command-map.md) maps the OpenTofu commands.
