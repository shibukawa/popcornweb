package pwcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shibukawa/popcornweb/internal/devconsole"
	"github.com/shibukawa/popcornweb/internal/pwenv"
	"github.com/shibukawa/popcornweb/internal/pwrequest"
)

const requestUsage = "usage: pw request [options] [METHOD] <path|operationId>\n" +
	"       pw request --list [--format=json]"

// requestOptions is everything the command line said. The curl spellings
// keep curl's meaning, so a line pasted from a shell history sends the same
// bytes; the options of pw request's own are the long ones with no curl twin.
type requestOptions struct {
	URL       string
	Env       string
	List      bool
	Format    string
	NoCatalog bool
	Strict    bool
	Identity  bool

	Method   string
	Data     []string
	Forms    []string
	Headers  []string
	Cookie   string
	Jar      string
	User     string
	Include  bool
	Output   string
	Fail     bool
	Silent   bool
	Follow   bool
	GetQuery bool
	JSON     bool
	Timeout  time.Duration

	Positional []string
}

// exitCodeError carries the exit status a command decided on, for the codes
// pw request shares with curl: 2 for usage, 22 for a failed status under -f.
type exitCodeError struct {
	code    int
	message string
}

func (e *exitCodeError) Error() string { return e.message }

func usageError(format string, args ...any) error {
	return &exitCodeError{code: 2, message: fmt.Sprintf(format, args...) + "\n" + requestUsage}
}

func parseRequestOptions(args []string) (requestOptions, error) {
	options := requestOptions{Format: "raw", Timeout: 30 * time.Second}
	// take reads the value of a flag that needs one: attached (-XPOST,
	// --url=x) or the next argument.
	take := func(index *int, attached string, hasAttached bool, name string) (string, error) {
		if hasAttached {
			return attached, nil
		}
		if *index+1 >= len(args) {
			return "", usageError("%s needs a value", name)
		}
		*index++
		return args[*index], nil
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			options.Positional = append(options.Positional, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			options.Positional = append(options.Positional, arg)
			continue
		}
		name, attached, hasAttached := arg, "", false
		if strings.HasPrefix(arg, "--") {
			name, attached, hasAttached = strings.Cut(arg, "=")
		} else if len(arg) > 2 {
			// A short flag with its value attached, as in -XPOST or -dname=x.
			name, attached, hasAttached = arg[:2], arg[2:], true
		}
		var err error
		var value string
		switch name {
		case "-X", "--request":
			value, err = take(&index, attached, hasAttached, name)
			options.Method = strings.ToUpper(value)
		case "-d", "--data", "--data-raw", "--data-binary":
			value, err = take(&index, attached, hasAttached, name)
			options.Data = append(options.Data, value)
		case "-F", "--form":
			value, err = take(&index, attached, hasAttached, name)
			options.Forms = append(options.Forms, value)
		case "-H", "--header":
			value, err = take(&index, attached, hasAttached, name)
			options.Headers = append(options.Headers, value)
		case "-b", "--cookie":
			options.Cookie, err = take(&index, attached, hasAttached, name)
		case "-c", "--cookie-jar":
			options.Jar, err = take(&index, attached, hasAttached, name)
		case "-u", "--user":
			options.User, err = take(&index, attached, hasAttached, name)
		case "-o", "--output":
			options.Output, err = take(&index, attached, hasAttached, name)
		case "--url":
			options.URL, err = take(&index, attached, hasAttached, name)
		case "--env":
			options.Env, err = take(&index, attached, hasAttached, name)
		case "--format":
			options.Format, err = take(&index, attached, hasAttached, name)
		case "--timeout":
			value, err = take(&index, attached, hasAttached, name)
			if err == nil {
				options.Timeout, err = time.ParseDuration(value)
				if err != nil {
					err = usageError("--timeout needs a duration such as 10s, got %q", value)
				}
			}
		case "-i", "--include":
			options.Include = true
		case "-f", "--fail":
			options.Fail = true
		case "-s", "--silent":
			options.Silent = true
		case "-L", "--location":
			options.Follow = true
		case "-G", "--get":
			options.GetQuery = true
		case "--json":
			options.JSON = true
		case "--list":
			options.List = true
		case "--no-catalog":
			options.NoCatalog = true
		case "--strict":
			options.Strict = true
		case "--identity":
			options.Identity = true
		case "-h", "--help":
			return options, usageError("pw request sends one request to this project's application")
		default:
			return options, usageError("unknown option %q", arg)
		}
		if err != nil {
			return options, err
		}
	}
	if options.Format != "raw" && options.Format != "json" {
		return options, usageError("--format needs raw or json, got %q", options.Format)
	}
	if options.List {
		if len(options.Positional) > 0 || len(options.Data) > 0 || len(options.Forms) > 0 {
			return options, usageError("--list prints the catalog and takes no target and no data")
		}
		return options, nil
	}
	switch len(options.Positional) {
	case 0:
		return options, usageError("name a path or an operation, or pass --list")
	case 1:
	case 2:
		if options.Method != "" {
			return options, usageError("the method is given twice, as %s and -X %s", options.Positional[0], options.Method)
		}
		if !isHTTPMethod(options.Positional[0]) {
			return options, usageError("%q is not an HTTP method", options.Positional[0])
		}
		options.Method = strings.ToUpper(options.Positional[0])
		options.Positional = options.Positional[1:]
	default:
		return options, usageError("one request per invocation; got %d targets", len(options.Positional))
	}
	if options.Identity {
		return options, usageError("--identity is not built yet; pass -H Authorization or -b for now")
	}
	if options.Identity && (options.Cookie != "" || options.User != "" || hasHeader(options.Headers, "Authorization")) {
		return options, usageError("--identity and an explicit credential name two identities for one request")
	}
	return options, nil
}

func isHTTPMethod(word string) bool {
	switch strings.ToUpper(word) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		return true
	}
	return false
}

func hasHeader(headers []string, name string) bool {
	for _, header := range headers {
		key, _, _ := strings.Cut(header, ":")
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return true
		}
	}
	return false
}

// requestTarget is the origin the request goes to and how it was found.
type requestTarget struct {
	Origin string
	// How is url, dev, or guess.
	How string
}

// requestEnvironment is what the command reads out of the project for one
// environment token: where the application would listen, where its document
// is, and which paths the guard protects.
type requestEnvironment struct {
	Env         string
	Port        int
	OpenAPIPath string
	APICatalog  bool
	Include     []string
	Exclude     []string
}

func readRequestEnvironment(root, env string) (requestEnvironment, error) {
	loaded, err := loadEnvironmentConfig(root, env, "", nil)
	if err != nil {
		return requestEnvironment{}, err
	}
	out := requestEnvironment{Env: env, OpenAPIPath: strings.TrimSpace(loaded.raw("server.openapi"))}
	if port, err := strconv.Atoi(strings.TrimSpace(loaded.raw("server.port"))); err == nil {
		out.Port = port
	}
	out.APICatalog = loaded.enabled("server.api_catalog")
	if loaded.enabled("auth.enabled") {
		out.Include = configList(loaded.raw("auth.protection.include"))
		out.Exclude = configList(loaded.raw("auth.protection.exclude"))
	}
	return out, nil
}

func runRequest(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	options, err := parseRequestOptions(args)
	if err != nil {
		return err
	}
	root, err := projectRoot(".")
	if err != nil {
		return fmt.Errorf("request: %w; pw request reaches this project's own application and nothing else", err)
	}
	env := options.Env
	if env == "" {
		env, err = pwenv.Resolve(nil)
		if err != nil {
			return err
		}
	}
	environment, err := readRequestEnvironment(root, env)
	if err != nil {
		return fmt.Errorf("request: read the %s configuration: %w", env, err)
	}
	client := &http.Client{Timeout: options.Timeout}
	target, err := resolveRequestTarget(ctx, root, options, environment, client)
	if err != nil {
		return err
	}
	catalog, catalogReason := loadRequestCatalog(ctx, client, target, environment, options)
	if options.List {
		if catalog == nil {
			return &exitCodeError{code: 2, message: "request: " + catalogReason}
		}
		return printRequestList(stdout, *catalog, options.Format, target)
	}

	input, err := requestInput(options)
	if err != nil {
		return err
	}
	operation, bindings, reason, err := selectOperation(catalog, catalogReason, options, input)
	if err != nil {
		return err
	}
	plan, err := pwrequest.Route(operation, bindings, input)
	if err != nil {
		return &exitCodeError{code: 2, message: "request: " + err.Error()}
	}
	plan.Reason = reason

	jar, err := loadCookieJar(options.Cookie)
	if err != nil {
		return err
	}
	cookieHeader := jar.header(target.Origin, plan.Path)
	if options.Cookie != "" && jar.isString {
		cookieHeader = options.Cookie
	}
	result, err := pwrequest.Send(ctx, client, target.Origin, plan, options.Follow, input.Headers, cookieHeader, options.User)
	if err != nil {
		return fmt.Errorf("request: %s %s: %w%s", plan.Method, target.Origin+plan.Path, err, notRunningHint(target))
	}
	if options.Jar != "" {
		if err := jar.saveResponse(options.Jar, target.Origin, result.Headers["Set-Cookie"]); err != nil {
			return fmt.Errorf("request: write cookie jar: %w", err)
		}
	}
	if err := printRequestResult(stdout, stderr, options, target, plan, result); err != nil {
		return err
	}
	if options.Fail && result.Status >= 400 {
		return &exitCodeError{code: 22, message: fmt.Sprintf("request: %s %s answered %d", plan.Method, plan.Path, result.Status)}
	}
	return nil
}

func notRunningHint(target requestTarget) string {
	if target.How == "guess" {
		return "; no application announced itself, so this was the configured port. Start it with pw dev, or pass --url"
	}
	return ""
}

// resolveRequestTarget picks the origin in the order decision:request-execution-target
// records: --url, the address the running pw dev loop announced, then the
// configured port as a guess.
func resolveRequestTarget(ctx context.Context, root string, options requestOptions, environment requestEnvironment, client *http.Client) (requestTarget, error) {
	if options.URL != "" {
		parsed, err := url.Parse(options.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return requestTarget{}, usageError("--url needs an http origin such as http://localhost:8080, got %q", options.URL)
		}
		if !loopbackHost(parsed.Hostname()) {
			return requestTarget{}, &exitCodeError{code: 1, message: fmt.Sprintf("request: --url %s is not loopback; pw request reaches this project's own application and nothing else", options.URL)}
		}
		return requestTarget{Origin: parsed.Scheme + "://" + parsed.Host, How: "url"}, nil
	}
	if listening := announcedApplication(ctx, root, client); listening != "" {
		return requestTarget{Origin: strings.TrimSuffix(listening, "/"), How: "dev"}, nil
	}
	if environment.Port > 0 {
		return requestTarget{Origin: "http://localhost:" + strconv.Itoa(environment.Port), How: "guess"}, nil
	}
	return requestTarget{}, &exitCodeError{code: 1, message: "request: no running application found and server.port is unset for " + environment.Env + "; start pw dev, or pass --url"}
}

// announcedApplication asks the pw dev console for the address the application
// announced. Anything short of an answer is an empty string: the console is a
// development convenience and its absence is the ordinary state outside a
// running loop.
func announcedApplication(ctx context.Context, root string, client *http.Client) string {
	config, err := loadProjectConfig(root)
	if err != nil || !config.Console.Enabled || config.Console.Port <= 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(config.Console.Port)+"/api/application", nil)
	if err != nil {
		return ""
	}
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var address devconsole.ApplicationAddress
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&address); err != nil {
		return ""
	}
	parsed, err := url.Parse(address.Listening)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func loopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loadRequestCatalog fetches the document the application serves and projects
// it. A nil catalog comes with the reason, which the report carries and which
// --list exits on.
func loadRequestCatalog(ctx context.Context, client *http.Client, target requestTarget, environment requestEnvironment, options requestOptions) (*pwrequest.Catalog, string) {
	if options.NoCatalog {
		return nil, "--no-catalog was given"
	}
	path := environment.OpenAPIPath
	if path == "" {
		if !environment.APICatalog {
			return nil, "server.openapi is unset for " + environment.Env + ", so the application serves no document to route by"
		}
		discovered, err := discoverOpenAPIPath(ctx, client, target.Origin)
		if err != nil {
			return nil, "the api-catalog at " + target.Origin + " named no OpenAPI document: " + err.Error()
		}
		path = discovered
	}
	document, err := fetchDocument(ctx, client, target.Origin+path)
	if err != nil {
		return nil, "the OpenAPI document at " + target.Origin + path + " could not be read: " + err.Error() + notRunningHint(target)
	}
	catalog, err := pwrequest.ParseOpenAPI(document)
	if err != nil {
		return nil, err.Error()
	}
	if len(environment.Include) > 0 {
		if err := catalog.MarkProtected(environment.Include, environment.Exclude); err != nil {
			return nil, "auth.protection patterns: " + err.Error()
		}
	}
	return &catalog, ""
}

// discoverOpenAPIPath follows the service-desc link of the RFC 9727 catalog
// the application serves at its well-known path.
func discoverOpenAPIPath(ctx context.Context, client *http.Client, origin string) (string, error) {
	body, err := fetchDocument(ctx, client, origin+"/.well-known/api-catalog")
	if err != nil {
		return "", err
	}
	var linkset struct {
		Linkset []struct {
			ServiceDesc []struct {
				Href string `json:"href"`
			} `json:"service-desc"`
		} `json:"linkset"`
	}
	if err := json.Unmarshal(body, &linkset); err != nil {
		return "", err
	}
	for _, context := range linkset.Linkset {
		for _, link := range context.ServiceDesc {
			parsed, err := url.Parse(link.Href)
			if err != nil || parsed.Path == "" {
				continue
			}
			return parsed.Path, nil
		}
	}
	return "", errors.New("no service-desc link")
}

func fetchDocument(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, application/linkset+json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 16<<20))
}

// requestInput turns the options into what routing reads.
func requestInput(options requestOptions) (pwrequest.Input, error) {
	input := pwrequest.Input{Method: options.Method, Query: options.GetQuery, JSON: options.JSON, Strict: options.Strict}
	if strings.HasPrefix(options.Positional[0], "/") {
		input.Path = options.Positional[0]
	}
	for _, data := range options.Data {
		if strings.HasPrefix(data, "@") || strings.HasPrefix(data, "{") || strings.HasPrefix(data, "[") || !strings.Contains(data, "=") {
			if input.RawSet {
				return input, usageError("more than one -d body; pairs are key=value")
			}
			input.Raw, input.RawSet = data, true
			continue
		}
		key, value, _ := strings.Cut(data, "=")
		input.Pairs = append(input.Pairs, pwrequest.Pair{Key: key, Value: value})
	}
	if input.RawSet && len(input.Pairs) > 0 {
		return input, usageError("a raw -d body cannot be combined with key=value pairs")
	}
	for _, form := range options.Forms {
		key, value, ok := strings.Cut(form, "=")
		if !ok {
			return input, usageError("-F needs field=value or field=@file, got %q", form)
		}
		input.Forms = append(input.Forms, pwrequest.Pair{Key: key, Value: value})
	}
	for _, header := range options.Headers {
		key, value, ok := strings.Cut(header, ":")
		if !ok {
			return input, usageError("-H needs Name: value, got %q", header)
		}
		input.Headers = append(input.Headers, pwrequest.Pair{Key: strings.TrimSpace(key), Value: strings.TrimSpace(value)})
	}
	return input, nil
}

// selectOperation finds what the target names in the catalog. A path nothing
// matches falls back to curl semantics with the reason recorded; an operation
// id nothing matches is an error, because the id was the whole point.
func selectOperation(catalog *pwrequest.Catalog, catalogReason string, options requestOptions, input pwrequest.Input) (*pwrequest.Operation, map[string]string, string, error) {
	targetName := options.Positional[0]
	if input.Path == "" {
		if catalog == nil {
			return nil, nil, "", &exitCodeError{code: 2, message: "request: " + targetName + " is an operation id, and " + catalogReason}
		}
		operation := catalog.ByID(targetName)
		if operation == nil {
			return nil, nil, "", &exitCodeError{code: 2, message: "request: no operation " + targetName + " in the catalog; pw request --list names them"}
		}
		return operation, nil, "", nil
	}
	if catalog == nil {
		return nil, nil, catalogReason, nil
	}
	hasData := len(input.Pairs) > 0 || input.RawSet || len(input.Forms) > 0
	method := input.Method
	if method == "" {
		method = "GET"
		if hasData && !input.Query {
			method = "POST"
		}
	}
	matches := catalog.Match(method, input.Path)
	if len(matches) == 0 && input.Method == "" {
		// The caller named no method, so a path that one operation serves under
		// another method is that operation rather than a curl default.
		if any := catalog.Match("", input.Path); len(any) == 1 {
			matches = any
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil, "no operation in the catalog matches " + method + " " + input.Path, nil
	case 1:
		return matches[0].Operation, matches[0].Bindings, "", nil
	}
	var names []string
	for _, match := range matches {
		names = append(names, match.Operation.Method+" "+match.Operation.Path+" ("+match.Operation.ID+")")
	}
	return nil, nil, "", &exitCodeError{code: 2, message: "request: " + input.Path + " matches more than one operation: " + strings.Join(names, ", ") + "; name one by id or add -X"}
}

func printRequestList(stdout io.Writer, catalog pwrequest.Catalog, format string, target requestTarget) error {
	if format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(struct {
			Origin     string                `json:"origin"`
			Operations []pwrequest.Operation `json:"operations"`
		}{Origin: target.Origin, Operations: catalog.Operations})
	}
	fmt.Fprintf(stdout, "%d operations at %s\n", len(catalog.Operations), target.Origin)
	for _, operation := range catalog.Operations {
		mark := ""
		if operation.Protected {
			mark = "  [protected]"
		}
		fmt.Fprintf(stdout, "\n%-6s %s  %s%s\n", operation.Method, operation.Path, operation.ID, mark)
		if operation.Summary != "" {
			fmt.Fprintf(stdout, "       %s\n", strings.SplitN(operation.Summary, "\n", 2)[0])
		}
		for _, parameter := range operation.Parameters {
			fmt.Fprintf(stdout, "       %s  %s\n", parameterLabel(parameter), parameter.In)
		}
		for _, field := range operation.Body {
			fmt.Fprintf(stdout, "       %s  body\n", parameterLabel(field))
		}
		if operation.ContentType != "" {
			fmt.Fprintf(stdout, "       body: %s\n", strings.Join(operation.ContentTypes, ", "))
		}
	}
	return nil
}

func parameterLabel(parameter pwrequest.Parameter) string {
	label := parameter.Name
	if parameter.Type != "" {
		label += ":" + parameter.Type
	}
	if parameter.Required {
		label += "*"
	}
	return label
}

func printRequestResult(stdout, stderr io.Writer, options requestOptions, target requestTarget, plan pwrequest.Plan, result pwrequest.Result) error {
	if options.Format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(pwrequest.NewReport(result, plan, target.How, target.Origin))
	}
	if !options.Silent {
		fmt.Fprint(stderr, routingReport(target, plan))
	}
	out := stdout
	if options.Output != "" {
		file, err := os.Create(options.Output)
		if err != nil {
			return fmt.Errorf("request: %w", err)
		}
		defer file.Close()
		out = file
	}
	if options.Include {
		fmt.Fprintf(out, "HTTP %d\n", result.Status)
		for _, line := range result.HeaderLines() {
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out)
	}
	if _, err := out.Write(result.Body); err != nil {
		return err
	}
	if options.Output == "" && len(result.Body) > 0 && result.Body[len(result.Body)-1] != '\n' {
		fmt.Fprintln(out)
	}
	return nil
}

// routingReport is the stderr line naming what was sent where, so a value
// that went to the query when the caller meant the body is visible here rather
// than only as a failing handler.
func routingReport(target requestTarget, plan pwrequest.Plan) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "pw request: %s %s via %s (%s)", plan.Method, plan.Path, target.Origin, target.How)
	if plan.Operation != nil {
		fmt.Fprintf(&builder, " as %s", plan.Operation.ID)
	} else if plan.Reason != "" {
		fmt.Fprintf(&builder, "; curl semantics: %s", plan.Reason)
	}
	builder.WriteString("\n")
	for _, routed := range plan.Routing {
		fmt.Fprintf(&builder, "  %s -> %s", routed.Key, routed.In)
		if routed.Note != "" {
			fmt.Fprintf(&builder, " (%s)", routed.Note)
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// cookieJar is the curl Netscape cookie file, read for -b and written for -c.
// Only the fields a loopback request needs are honoured: name, value, and
// path; the domain is the application's, because that is the only origin the
// command reaches.
type cookieJar struct {
	isString bool
	entries  []jarEntry
}

type jarEntry struct {
	Path  string
	Name  string
	Value string
}

func loadCookieJar(cookie string) (cookieJar, error) {
	if cookie == "" {
		return cookieJar{}, nil
	}
	// curl decides the same way: a value with = is a cookie string, anything
	// else names a file.
	if strings.Contains(cookie, "=") {
		return cookieJar{isString: true}, nil
	}
	source, err := os.ReadFile(cookie)
	if err != nil {
		return cookieJar{}, fmt.Errorf("request: read cookie jar: %w", err)
	}
	jar := cookieJar{}
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#HttpOnly_"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		jar.entries = append(jar.entries, jarEntry{Path: fields[2], Name: fields[5], Value: fields[6]})
	}
	return jar, nil
}

func (j cookieJar) header(origin, path string) string {
	var parts []string
	for _, entry := range j.entries {
		if entry.Path != "" && entry.Path != "/" && !strings.HasPrefix(path, entry.Path) {
			continue
		}
		parts = append(parts, entry.Name+"="+entry.Value)
	}
	return strings.Join(parts, "; ")
}

func (j cookieJar) saveResponse(name, origin string, setCookies []string) error {
	entries := map[string]jarEntry{}
	for _, entry := range j.entries {
		entries[entry.Name] = entry
	}
	for _, header := range setCookies {
		response := http.Response{Header: http.Header{"Set-Cookie": []string{header}}}
		for _, cookie := range response.Cookies() {
			if cookie.MaxAge < 0 {
				delete(entries, cookie.Name)
				continue
			}
			path := cookie.Path
			if path == "" {
				path = "/"
			}
			entries[cookie.Name] = jarEntry{Path: path, Name: cookie.Name, Value: cookie.Value}
		}
	}
	host := "localhost"
	if parsed, err := url.Parse(origin); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	names := make([]string, 0, len(entries))
	for cookieName := range entries {
		names = append(names, cookieName)
	}
	sort.Strings(names)
	var builder strings.Builder
	builder.WriteString("# Netscape HTTP Cookie File\n# written by pw request\n")
	for _, cookieName := range names {
		entry := entries[cookieName]
		fmt.Fprintf(&builder, "%s\tFALSE\t%s\tFALSE\t0\t%s\t%s\n", host, entry.Path, entry.Name, entry.Value)
	}
	return os.WriteFile(name, []byte(builder.String()), 0o600)
}
