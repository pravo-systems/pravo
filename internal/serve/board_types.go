package serve

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/pravo-systems/pravo/internal/schema"
)

// The types of the board: what the one bar finds when a person types what they want ("a database", "bucket"), and
// the form of one type, built from its provider schema, with the attributes that can be wired.

// BoardTypes is the answer of GET /api/board/types?q=WORDS.
type BoardTypes struct {
	Version int            `json:"version"`
	Query   string         `json:"query"`
	Hits    []BoardTypeHit `json:"hits"`
	// Total is how many types the providers of the board have.
	Total int `json:"total"`
}

// BoardTypeHit is one type the bar offers: a resource type of a provider the specification declares, or a template.
type BoardTypeHit struct {
	// Kind is "resource" or "template".
	Kind string `json:"kind"`
	// Type is the resource type, or the name of the template.
	Type string `json:"type"`
	// Title is the plain name: "Virtual machine" for aws_instance.
	Title string `json:"title"`
	// Provider is the struct of the provider that runs it, such as $aws; empty for a template.
	Provider string `json:"provider,omitempty"`
	// Blurb is the first sentence of what the schema or the template says of it.
	Blurb string `json:"blurb,omitempty"`
}

// BoardType is the answer of GET /api/board/type?type=TYPE&provider=$aws: the form of a resource type.
type BoardType struct {
	Version  int    `json:"version"`
	Kind     string `json:"kind"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Provider string `json:"provider,omitempty"`
	Doc      string `json:"doc,omitempty"`
	// Fields are what a person sets: the required ones first, then the rest, each in the order of the schema.
	Fields []BoardField `json:"fields"`
	// Reads are the string attributes of the type that another resource can read: id, arn, name, and so on.
	Reads []string `json:"reads"`
	// Label is the name the board gives a new one: "db" for a database, "bucket" for a bucket.
	Label string `json:"label"`
}

// BoardField is one attribute or nested block that the form asks for.
type BoardField struct {
	Name string `json:"name"`
	// Kind says which input the form draws: text, number, bool, list (of strings), or expr (CUE as written: a map, an
	// object, a list of objects, a nested block).
	Kind string `json:"kind"`
	// Type is the type as the schema says it: string, list(string), block.
	Type     string `json:"type"`
	Doc      string `json:"doc,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Computed is set when the provider fills it in if it is left out.
	Computed   bool `json:"computed,omitempty"`
	Sensitive  bool `json:"sensitive,omitempty"`
	WriteOnly  bool `json:"write_only,omitempty"`
	Deprecated bool `json:"deprecated,omitempty"`
	// Default is the CUE of the default of a template input.
	Default string `json:"default,omitempty"`
	// Options are the values an enum of a template input allows, as CUE.
	Options []string `json:"options,omitempty"`
	// Wire says what the field can be wired to: its socket.
	Wire *BoardWire `json:"wire,omitempty"`
}

// BoardWire is what a socket can take: the types it can read, the likeliest first, with the attribute each read.
type BoardWire struct {
	To   []BoardTarget `json:"to"`
	Many bool          `json:"many,omitempty"`
}

// BoardTarget is a type a socket can read, and what it reads of it.
type BoardTarget struct {
	Type string `json:"type"`
	Read string `json:"read"`
}

// boardNames are the plain names of the types that are drawn most, and the words a person types for them. A type that
// is not here is named from its type: aws_db_subnet_group is "DB subnet group".
var boardNames = map[string]struct {
	title string
	words string
	label string
}{
	"aws_instance":                {"Virtual machine", "vm server machine compute ec2 host", "vm"},
	"aws_vpc":                     {"Network", "vpc network net", "network"},
	"aws_subnet":                  {"Subnet", "subnet network", "subnet"},
	"aws_security_group":          {"Firewall", "firewall security group sg ports", "firewall"},
	"aws_db_instance":             {"Database", "database db rds postgres mysql sql", "db"},
	"aws_rds_cluster":             {"Database cluster", "database db aurora cluster rds", "db"},
	"aws_db_subnet_group":         {"Database subnets", "database db subnet group", "db_subnets"},
	"aws_s3_bucket":               {"Bucket", "bucket storage files s3 objects", "bucket"},
	"aws_lambda_function":         {"Function", "function lambda serverless code", "fn"},
	"aws_iam_role":                {"Role", "role iam permission identity", "role"},
	"aws_iam_policy":              {"Permission policy", "iam policy permission", "permissions"},
	"aws_iam_user":                {"User", "user iam person", "user"},
	"aws_sqs_queue":               {"Queue", "queue sqs messages", "queue"},
	"aws_sns_topic":               {"Topic", "topic sns notifications pubsub", "topic"},
	"aws_lb":                      {"Load balancer", "load balancer lb alb nlb", "lb"},
	"aws_lb_target_group":         {"Load balancer targets", "target group lb", "targets"},
	"aws_lb_listener":             {"Load balancer listener", "listener lb port", "listener"},
	"aws_route53_zone":            {"DNS zone", "dns zone domain route53", "zone"},
	"aws_route53_record":          {"DNS record", "dns record domain route53 name", "record"},
	"aws_acm_certificate":         {"Certificate", "certificate tls ssl https acm", "cert"},
	"aws_kms_key":                 {"Encryption key", "key kms encryption", "key"},
	"aws_secretsmanager_secret":   {"Secret", "secret password credential", "secret"},
	"aws_ecs_cluster":             {"Container cluster", "container cluster ecs docker", "cluster"},
	"aws_ecs_service":             {"Container service", "container service ecs docker", "service"},
	"aws_eks_cluster":             {"Kubernetes cluster", "kubernetes k8s cluster eks", "cluster"},
	"aws_ecr_repository":          {"Container registry", "registry container image ecr docker", "registry"},
	"aws_dynamodb_table":          {"Table", "table dynamodb nosql database", "table"},
	"aws_elasticache_cluster":     {"Cache", "cache redis memcached elasticache", "cache"},
	"aws_cloudfront_distribution": {"CDN", "cdn cloudfront edge distribution", "cdn"},
	"aws_cloudwatch_log_group":    {"Log group", "logs log group cloudwatch", "logs"},
	"aws_internet_gateway":        {"Internet gateway", "internet gateway igw", "igw"},
	"aws_nat_gateway":             {"NAT gateway", "nat gateway", "nat"},
	"aws_eip":                     {"Public IP", "ip address elastic eip public", "ip"},
	"aws_route_table":             {"Route table", "routes route table", "routes"},
	"aws_api_gateway_rest_api":    {"API", "api gateway rest http", "api"},
	"aws_apigatewayv2_api":        {"HTTP API", "api gateway http websocket", "api"},
	"aws_key_pair":                {"SSH key", "ssh key pair login", "ssh_key"},
	"aws_efs_file_system":         {"File system", "file system nfs efs disk", "files"},
	"aws_ebs_volume":              {"Disk", "disk volume ebs block storage", "disk"},

	"google_compute_instance":             {"Virtual machine", "vm server machine compute gce host", "vm"},
	"google_compute_network":              {"Network", "vpc network net", "network"},
	"google_compute_subnetwork":           {"Subnet", "subnet subnetwork network", "subnet"},
	"google_compute_firewall":             {"Firewall", "firewall ports rules", "firewall"},
	"google_sql_database_instance":        {"Database", "database db sql cloud postgres mysql", "db"},
	"google_sql_database":                 {"Database schema", "database db sql schema", "schema"},
	"google_storage_bucket":               {"Bucket", "bucket storage files gcs objects", "bucket"},
	"google_cloudfunctions2_function":     {"Function", "function serverless code cloud functions", "fn"},
	"google_cloud_run_v2_service":         {"Container service", "container service cloud run serverless", "service"},
	"google_container_cluster":            {"Kubernetes cluster", "kubernetes k8s cluster gke container", "cluster"},
	"google_service_account":              {"Service account", "service account identity role", "account"},
	"google_pubsub_topic":                 {"Topic", "topic pubsub messages", "topic"},
	"google_pubsub_subscription":          {"Subscription", "subscription pubsub queue messages", "sub"},
	"google_dns_managed_zone":             {"DNS zone", "dns zone domain", "zone"},
	"google_dns_record_set":               {"DNS record", "dns record domain", "record"},
	"google_kms_crypto_key":               {"Encryption key", "key kms encryption crypto", "key"},
	"google_secret_manager_secret":        {"Secret", "secret password credential", "secret"},
	"google_artifact_registry_repository": {"Container registry", "registry container image artifact docker", "registry"},
	"google_redis_instance":               {"Cache", "cache redis memorystore", "cache"},

	"azurerm_resource_group":             {"Resource group", "resource group rg", "group"},
	"azurerm_virtual_network":            {"Network", "vnet network net", "network"},
	"azurerm_subnet":                     {"Subnet", "subnet network", "subnet"},
	"azurerm_linux_virtual_machine":      {"Virtual machine", "vm server machine linux host", "vm"},
	"azurerm_storage_account":            {"Storage account", "storage bucket blob files account", "storage"},
	"azurerm_kubernetes_cluster":         {"Kubernetes cluster", "kubernetes k8s cluster aks", "cluster"},
	"azurerm_postgresql_flexible_server": {"Database", "database db postgres sql", "db"},
	"azurerm_key_vault":                  {"Key vault", "key vault secret", "vault"},

	"random_pet":      {"Random name", "random name pet word", "name"},
	"random_password": {"Random password", "random password secret", "password"},
	"random_integer":  {"Random number", "random number integer port", "number"},
	"random_string":   {"Random string", "random string text", "text"},
	"random_id":       {"Random id", "random id bytes", "rid"},
	"local_file":      {"File", "file local text disk", "file"},
	"null_resource":   {"Step", "null step trigger", "step"},
}

// boardAcronyms are words of a type name that are written in capitals in its plain name.
var boardAcronyms = map[string]string{
	"db": "DB", "rds": "RDS", "s3": "S3", "iam": "IAM", "vpc": "VPC", "dns": "DNS", "ec2": "EC2", "ecs": "ECS", "eks": "EKS",
	"sqs": "SQS", "sns": "SNS", "kms": "KMS", "acm": "ACM", "lb": "LB", "ip": "IP", "api": "API", "ebs": "EBS", "efs": "EFS",
	"sql": "SQL", "gke": "GKE", "cdn": "CDN", "nat": "NAT", "tls": "TLS", "ssl": "SSL", "id": "id", "url": "URL", "http": "HTTP",
	"vpn": "VPN", "waf": "WAF", "ssm": "SSM", "ecr": "ECR", "msk": "MSK", "emr": "EMR", "dx": "DX", "ses": "SES", "iot": "IoT",
}

// boardTitle is the plain name of a type.
func boardTitle(typ string) string {
	if n, ok := boardNames[typ]; ok {
		return n.title
	}
	words := strings.Split(strings.TrimPrefix(typ, prefixOf(typ)+"_"), "_")
	for i, w := range words {
		if a, ok := boardAcronyms[w]; ok {
			words[i] = a
		} else if i == 0 && w != "" {
			r := []rune(w)
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}

// boardLabelFor is the name the board gives a new resource of the type: its short name, or the last word of the type.
func boardLabelFor(typ string) string {
	if n, ok := boardNames[typ]; ok {
		return n.label
	}
	words := strings.Split(typ, "_")
	l := words[len(words)-1]
	if len(words) > 2 && (l == "instance" || l == "group" || l == "version" || l == "policy" || l == "configuration" || l == "attachment" || l == "association") {
		l = words[len(words)-2] + "_" + l
	}
	if !boardLabelOK.MatchString(l) || cueKeywords[l] {
		l = "r_" + l
	}
	return l
}

// boardTypeScore says how well a type answers the words of the bar: 0 when it does not.
func boardTypeScore(typ, title string, q []string) int {
	if len(q) == 0 {
		if _, ok := boardNames[typ]; ok {
			return 10
		}
		return 0
	}
	short := strings.TrimPrefix(typ, prefixOf(typ)+"_")
	words := strings.Split(short, "_")
	known, popular := boardNames[typ]
	hay := strings.ToLower(title)
	score := 0
	for _, w := range q {
		switch {
		case typ == w || short == w:
			score += 200
		case strings.HasPrefix(typ, w) || strings.HasPrefix(short, w):
			score += 60
		case popular && containsWord(known.words, w):
			score += 80
		case popular && containsPrefix(known.words, w):
			score += 40
		case containsPrefix(hay, w):
			score += 30
		case func() bool {
			for _, x := range words {
				if strings.HasPrefix(x, w) {
					return true
				}
			}
			return false
		}():
			score += 20
		case len(w) >= 3 && strings.Contains(typ, w):
			score += 8
		default:
			return 0
		}
	}
	if popular {
		score += 25
	}
	// a short name is a more general thing: the instance before its attachments
	score -= len(words) * 2
	return score
}

func containsWord(words, w string) bool {
	for _, x := range strings.Fields(words) {
		if x == w {
			return true
		}
	}
	return false
}

func containsPrefix(text, w string) bool {
	for _, x := range strings.Fields(text) {
		if strings.HasPrefix(x, w) {
			return true
		}
	}
	return false
}

// boardTypesHandler answers GET /api/board/types?q=WORDS.
func (s *Server) boardTypesHandler(w http.ResponseWriter, r *http.Request, m *model) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(q) > 120 {
		q = q[:120]
	}
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ", ",", " ").Replace(q))
	if len(words) > 6 {
		words = words[:6]
	}
	// "a database", "an s3 bucket": the article is not a word to look for
	var kept []string
	for _, w := range words {
		if w != "a" && w != "an" && w != "the" && w != "new" {
			kept = append(kept, w)
		}
	}
	out := &BoardTypes{Version: APIVersion, Query: q, Hits: []BoardTypeHit{}}
	type scored struct {
		hit   BoardTypeHit
		score int
	}
	var all []scored
	for _, p := range s.boardProviders(m) {
		out.Total += len(p.types)
		for _, typ := range p.types {
			title := boardTitle(typ)
			if sc := boardTypeScore(typ, title, kept); sc > 0 {
				all = append(all, scored{BoardTypeHit{Kind: "resource", Type: typ, Title: title, Provider: p.Label}, sc})
			}
		}
	}
	for _, t := range s.boardTemplates(m) {
		out.Total++
		title := strings.ToLower(t.Title + " " + t.Name + " " + t.Description)
		sc := 30
		for _, w := range kept {
			if !strings.Contains(title, w) {
				sc = 0
				break
			}
			sc += 50
		}
		if sc > 0 {
			all = append(all, scored{BoardTypeHit{Kind: "template", Type: t.Name, Title: t.Title, Blurb: clip(oneLine(t.Description), 160)}, sc + 40})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].hit.Type < all[j].hit.Type
	})
	for i := 0; i < len(all) && i < 24; i++ {
		h := all[i].hit
		if h.Kind == "resource" && i < 8 {
			if sch := s.boardSchema(m, h.Provider, h.Type); sch != nil && sch.Block != nil {
				h.Blurb = firstSentence(sch.Block.Description)
			}
		}
		out.Hits = append(out.Hits, h)
	}
	writeJSON(w, http.StatusOK, out)
}

var (
	mdLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdEmph   = regexp.MustCompile(`(^|[\s(])(?:\*\*|__|\*|_)([^*_\s][^*_]*?)(?:\*\*|__|\*|_)`)
	mdNote   = regexp.MustCompile(`^(?:[-~!]>|\*\*(?:Note|NOTE|Warning|WARNING)\b|(?:Note|NOTE|Warning|WARNING):)`)
	mdMarker = regexp.MustCompile(`^(?:[-~!]> ?|\*\*[A-Za-z ]+:?\*\*:? ?)+`)
)

// firstSentence is the first sentence of a description, on one line and clipped, without the marks of Markdown that
// provider documentation carries: a link is its text, a note marker (-> or ~>) is dropped.
func firstSentence(d string) string {
	// a provider's description often opens with a note ("-> If the managed resource supports..."): the first paragraph
	// that is not one says what the thing is
	if paras := strings.Split(strings.TrimSpace(d), "\n\n"); len(paras) > 1 {
		for _, p := range paras {
			if p = strings.TrimSpace(p); p != "" && !mdNote.MatchString(p) {
				d = p
				break
			}
		}
	}
	d = oneLine(d)
	d = mdMarker.ReplaceAllString(mdLink.ReplaceAllString(d, "$1"), "")
	d = mdEmph.ReplaceAllString(strings.ReplaceAll(d, "`", ""), "$1$2")
	if i := strings.Index(d, ". "); i > 0 {
		d = d[:i+1]
	}
	return clip(d, 240)
}

// boardTypeHandler answers GET /api/board/type?type=TYPE&provider=$aws, and ?template=NAME for a template.
func (s *Server) boardTypeHandler(w http.ResponseWriter, r *http.Request, m *model) {
	q := r.URL.Query()
	if name := q.Get("template"); name != "" {
		t := s.boardTemplate(m, name)
		if t == nil {
			writeError(w, http.StatusNotFound, "no such template")
			return
		}
		writeJSON(w, http.StatusOK, boardTemplateForm(t))
		return
	}
	typ, prov := q.Get("type"), q.Get("provider")
	p := s.boardProvider(m, prov, typ)
	if p == nil {
		writeError(w, http.StatusNotFound, "no provider of the specification has this type")
		return
	}
	sch := s.boardSchema(m, p.Label, typ)
	if sch == nil || sch.Block == nil {
		writeError(w, http.StatusNotFound, "the schema of this type is not cached")
		return
	}
	writeJSON(w, http.StatusOK, s.boardForm(m, p, typ, sch.Block))
}

// boardForm is the form of a resource type.
func (s *Server) boardForm(m *model, p *boardProv, typ string, b *schema.Block) *BoardType {
	out := &BoardType{Version: APIVersion, Kind: "resource", Type: typ, Title: boardTitle(typ), Provider: p.Label, Doc: firstSentence(b.Description),
		Fields: []BoardField{}, Reads: []string{}, Label: boardLabelFor(typ)}
	ix := s.boardWires(m)
	var req, rest []BoardField
	for _, a := range b.Attributes {
		if attrType(a) == "string" && a.Name != "" {
			out.Reads = append(out.Reads, a.Name)
		}
		if !a.Required && !a.Optional {
			continue // only the provider sets it
		}
		f := BoardField{Name: a.Name, Type: attrType(a), Doc: firstSentence(a.Description), Required: a.Required, Computed: a.Computed,
			Sensitive: a.Sensitive, WriteOnly: a.WriteOnly, Deprecated: a.Deprecated}
		switch f.Type {
		case "string":
			f.Kind = "text"
		case "number":
			f.Kind = "number"
		case "bool":
			f.Kind = "bool"
		case "list(string)", "set(string)":
			f.Kind = "list"
		default:
			f.Kind = "expr"
		}
		if h := ix.wireOf(typ, a); h != nil {
			f.Wire = &BoardWire{Many: h.Many}
			for _, t := range h.To {
				f.Wire.To = append(f.Wire.To, BoardTarget(t))
			}
		}
		if f.Required {
			req = append(req, f)
		} else {
			rest = append(rest, f)
		}
	}
	for _, nb := range b.BlockTypes {
		f := BoardField{Name: nb.TypeName, Kind: "expr", Type: "block", Doc: firstSentence(nb.Block.Description), Required: nb.MinItems > 0}
		if nb.Nesting == schema.NestingList || nb.Nesting == schema.NestingSet {
			f.Type = "list(block)"
		}
		if f.Required {
			req = append(req, f)
		} else {
			rest = append(rest, f)
		}
	}
	out.Fields = append(append(out.Fields, req...), rest...)
	return out
}

// boardTemplateForm is the form of a template: its inputs, and a socket for each that picks a resource.
func boardTemplateForm(t *TemplateEntry) *BoardType {
	out := &BoardType{Version: APIVersion, Kind: "template", Type: t.Name, Title: t.Title, Doc: firstSentence(t.Description), Fields: []BoardField{}, Reads: []string{},
		Label: boardTemplateLabel(t.Name)}
	for _, in := range t.Inputs {
		f := BoardField{Name: in.Name, Type: in.Kind, Doc: firstSentence(in.Doc), Required: in.Required, Default: in.Default, Options: in.Options, Sensitive: in.Secret}
		switch in.Kind {
		case "string":
			f.Kind = "text"
		case "int", "number":
			f.Kind = "number"
		case "bool":
			f.Kind = "bool"
		default:
			f.Kind = "expr"
		}
		if in.Entity != nil {
			f.Wire = &BoardWire{To: []BoardTarget{{Type: in.Entity.Type, Read: in.Entity.Attr}}}
		}
		out.Fields = append(out.Fields, f)
	}
	return out
}

// boardTemplateLabel is the name the board gives a new instance of a template.
func boardTemplateLabel(name string) string {
	l := strings.ToLower(strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '.' {
			return '_'
		}
		return r
	}, name))
	if !boardLabelOK.MatchString(l) || cueKeywords[l] {
		return "instance"
	}
	return l
}
