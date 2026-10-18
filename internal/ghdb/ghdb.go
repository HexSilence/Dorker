package ghdb

import (
	"fmt"
	"net"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

type Entry struct {
	ID            int
	Title         string
	Description   string
	Category      string
	QueryTemplate string
	Tags          []string
}

var catalog = []Entry{
	{1, "PDF documents", "Find indexed PDF documents.", "Documents", `{{domain}} ext:pdf`, []string{"files", "reports"}},
	{2, "Office documents", "Find indexed office documents.", "Documents", `{{domain}} (ext:doc OR ext:docx OR ext:xls OR ext:xlsx OR ext:ppt OR ext:pptx)`, []string{"files", "office"}},
	{3, "Login pages", "Find pages that look like login portals.", "Login pages", `{{domain}} (inurl:login OR intitle:"sign in")`, []string{"authentication", "portal"}},
	{4, "Admin panels", "Find pages that look like administration panels.", "Admin panels", `{{domain}} (inurl:admin OR intitle:"admin panel")`, []string{"administration", "portal"}},
	{5, "Directory listings", "Find indexed directory listings.", "Directory listings", `{{domain}} intitle:"index of"`, []string{"index", "files"}},
	{6, "Configuration files", "Find indexed configuration-related files.", "Configuration exposure", `{{domain}} (ext:conf OR ext:config OR ext:ini OR ext:yaml OR ext:yml)`, []string{"configuration", "files"}},
	{7, "Environment files", "Find references to environment configuration files.", "Configuration exposure", `{{domain}} (inurl:.env OR ext:env)`, []string{"configuration", "environment"}},
	{8, "Backup files", "Find common indexed backup file types.", "Backup files", `{{domain}} (ext:bak OR ext:backup OR ext:old OR ext:orig)`, []string{"archive", "files"}},
	{9, "Archive files", "Find indexed compressed archives.", "Backup files", `{{domain}} (ext:zip OR ext:tar OR ext:gz OR ext:7z)`, []string{"archive", "files"}},
	{10, "Database files", "Find references to common database files.", "Database-related pages", `{{domain}} (ext:sql OR ext:sqlite OR ext:mdb)`, []string{"database", "files"}},
	{11, "Database errors", "Find pages containing common database error messages.", "Error messages", `{{domain}} ("SQL syntax" OR "database error" OR "ODBC error")`, []string{"database", "errors"}},
	{12, "Application errors", "Find indexed application error pages.", "Error messages", `{{domain}} (intitle:"error" OR "stack trace" OR "exception")`, []string{"errors", "debug"}},
	{13, "Debug pages", "Find pages that reference debug or diagnostics output.", "Development files", `{{domain}} (inurl:debug OR intitle:"debug")`, []string{"development", "diagnostics"}},
	{14, "Source maps", "Find indexed JavaScript source maps.", "Source/code references", `{{domain}} ext:map`, []string{"javascript", "source"}},
	{15, "Source repositories", "Find references to exposed repository metadata.", "Source/code references", `{{domain}} (inurl:.git OR inurl:.svn)`, []string{"git", "repository"}},
	{16, "API documentation", "Find public API documentation pages.", "API documentation", `{{domain}} (inurl:swagger OR inurl:openapi OR intitle:"API documentation")`, []string{"api", "swagger", "openapi"}},
	{17, "GraphQL pages", "Find indexed GraphQL documentation or consoles.", "API documentation", `{{domain}} (inurl:graphql OR intitle:"GraphQL")`, []string{"api", "graphql"}},
	{18, "Public indexes", "Find public index and catalog pages.", "Public indexes", `{{domain}} (intitle:"file index" OR intitle:"document index")`, []string{"index", "catalog"}},
	{19, "Log files", "Find indexed application and server logs.", "Development files", `{{domain}} ext:log`, []string{"logs", "diagnostics"}},
	{20, "Robots files", "Find indexed robots.txt files.", "Technology fingerprints", `{{domain}} inurl:robots.txt`, []string{"robots", "discovery"}},
	{21, "Server status pages", "Find public server status pages.", "Technology fingerprints", `{{domain}} (intitle:"server status" OR inurl:server-status)`, []string{"server", "status"}},
	{22, "CMS fingerprints", "Find pages exposing common CMS markers.", "Technology fingerprints", `{{domain}} (inurl:wp-content OR inurl:sites/default OR inurl:administrator)`, []string{"cms", "technology"}},
	{23, "Cloud storage references", "Find pages referencing common cloud storage services.", "Cloud/storage references", `{{domain}} ("s3.amazonaws.com" OR "storage.googleapis.com" OR "blob.core.windows.net")`, []string{"cloud", "storage"}},
	{24, "Development environments", "Find pages referring to staging or development environments.", "Development files", `{{domain}} (inurl:staging OR inurl:dev OR intitle:"development")`, []string{"staging", "development"}},
	{25, "Documentation sites", "Find indexed technical documentation sites.", "Documents", `{{domain}} (inurl:docs OR intitle:"documentation")`, []string{"documentation", "technical"}},
}

func All() []Entry { return append([]Entry(nil), catalog...) }

func ByID(id int) (Entry, bool) {
	if id < 1 || id > len(catalog) || catalog[id-1].ID != id {
		return Entry{}, false
	}
	return catalog[id-1], true
}

func Category(name string) []Entry {
	name = strings.TrimSpace(name)
	var out []Entry
	for _, e := range catalog {
		if strings.EqualFold(e.Category, name) {
			out = append(out, e)
		}
	}
	return out
}

func Find(term string) []Entry {
	term = strings.ToLower(strings.TrimSpace(term))
	var out []Entry
	for _, e := range catalog {
		haystack := strings.ToLower(e.Title + " " + e.Description + " " + strings.Join(e.Tags, " "))
		if term != "" && strings.Contains(haystack, term) {
			out = append(out, e)
		}
	}
	return out
}

func Expand(e Entry, domain string) (string, error) {
	domain, err := normalizeDomain(domain)
	if err != nil {
		return "", err
	}
	replacement := ""
	if domain != "" {
		replacement = "site:" + domain
	}
	return strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(e.QueryTemplate, "{{domain}}", replacement)), " ")), nil
}

func normalizeDomain(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", fmt.Errorf("domain must not contain spaces or control characters")
		}
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if strings.ContainsAny(value, "/:@?#") {
		return "", fmt.Errorf("enter a domain name, not a URL")
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String(), nil
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil || len(ascii) > 253 {
		return "", fmt.Errorf("invalid domain %q", value)
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("domain must contain a dot")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid domain %q", value)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return "", fmt.Errorf("invalid domain %q", value)
			}
		}
	}
	return ascii, nil
}
