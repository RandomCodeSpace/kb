package forge

import (
	"context"
	"net/http"
	"net/url"

	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	MaxImportIssues    = maxImportIssues
	MaxForgeCommentLen = maxForgeCommentLen
	MaxForgeComments   = maxForgeComments
)

type HTTPResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

func (s *Service) Get(ctx context.Context, ref Ref, base, path string, query url.Values) (HTTPResponse, error) {
	response, err := s.forgeGet(ctx, privateRef(ref), base, path, query)
	return HTTPResponse{Status: response.status, Header: response.header, Body: response.body}, err
}

func (s *Service) FetchComments(ctx context.Context, ref Ref, base, path string) ([]string, error) {
	return s.fetchForgeComments(ctx, privateRef(ref), base, path)
}

func (s *Service) FetchIssueSnapshot(ctx context.Context, ref Ref) (Issue, string, string, error) {
	return s.fetchIssueSnapshot(ctx, privateRef(ref))
}

func (s *Service) IssuesList(ctx context.Context, ref Ref, base string) (string, url.Values, error) {
	return s.forgeIssuesList(ctx, privateRef(ref), base)
}

func ParseIssueList(kind string, body []byte) ([]Issue, error) {
	return parseForgeIssueList(kind, body)
}
func ProjectPath(ref Ref) (string, error)       { return forgeProjectPath(privateRef(ref)) }
func IssuePath(ref Ref) (string, error)         { return forgeIssuePath(privateRef(ref)) }
func MilestonePath(ref Ref) (string, error)     { return forgeMilestonePath(privateRef(ref)) }
func ProjectIssuesPath(ref Ref) (string, error) { return forgeProjectIssuesPath(privateRef(ref)) }
func TotalHint(header http.Header) int          { return forgeTotalHint(header) }
func ParseGitLabRef(source store.ForgeSource, path string) (Ref, error) {
	value, err := parseGitLabRef(source, path)
	return publicRef(value), err
}
func ParseGitHubRef(source store.ForgeSource, path string) (Ref, error) {
	value, err := parseGitHubRef(source, path)
	return publicRef(value), err
}
func IssueListRequest(ref Ref) (string, url.Values, error) {
	return forgeIssueListRequest(privateRef(ref))
}
func SetMilestoneQuery(kind string, query url.Values, body []byte) bool {
	return setForgeMilestoneQuery(kind, query, body)
}
func ParseComments(kind string, body []byte) ([]string, error) { return parseForgeComments(kind, body) }
func AppendBoundedComment(comments []string, body string) []string {
	return appendBoundedForgeComment(comments, body)
}
func APIBase(kind, base string) (string, error)       { return forgeAPIBase(kind, base) }
func AppendImportNote(note, extra string) string      { return appendImportNote(note, extra) }
func RefKind(ref Ref) string                          { return refKind(ref) }
func PackIssues(issues []Issue) (string, int)         { return packImportIssues(issues) }
func IssueADR(issue Issue) string                     { return forgeIssueADR(issue) }
func TruncateText(text string, max int) string        { return truncateImportText(text, max) }
func RefID(raw string) (int, error)                   { return forgeRefID(raw) }
func NormalizeProbeBase(raw string) (*url.URL, error) { return normalizeForgeProbeBase(raw) }
func URLPort(value *url.URL) string                   { return forgeURLPort(value) }
func DrainResponse(response *http.Response) error     { return drainForgeResponse(response) }
func ValidSourceName(name string) bool                { return validForgeSourceName(name) }
func DriftRevision(baseline store.ImportBaseline) string {
	return importDriftRevision(baseline)
}
func NewTestRequest(ctx context.Context, kind, baseURL, token, project string) (*http.Request, error) {
	return newForgeTestRequest(ctx, kind, forgeTestTarget{baseURL: baseURL, pat: token}, project)
}
func ExecuteTest(client *http.Client, request *http.Request) error {
	return executeForgeTest(client, request)
}
func ValidDriftRevision(revision string) bool { return validImportDriftRevision(revision) }
