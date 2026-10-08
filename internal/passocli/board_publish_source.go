package passocli

import (
	"bytes"
	"encoding/json"
	"github.com/pyxcloud/pyxcloud-cli/internal/passostate"
	"io"
	"regexp"
	"strconv"
	"strings"
)

func publishSourceInput(raw json.RawMessage) error {
	fail := func() error { return &ExitError{20, "invalid_board_input"} }
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return fail()
	}
	if !d.More() {
		return fail()
	}
	token, err = d.Token()
	if err != nil || token != "versionLabel" {
		return fail()
	}
	var label string
	if d.Decode(&label) != nil || !passostate.ValidVersionLabel(label) || d.More() {
		return fail()
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || d.Decode(&struct{}{}) != io.EOF {
		return fail()
	}
	return nil
}

var publishedGitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var publishedRepository = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func validatePublishedSource(params map[string]string, input map[string]json.RawMessage, body json.RawMessage) error {
	fail := func() error { return &ExitError{30, "published_source_scope_mismatch"} }
	var receipt struct {
		ProjectID           int64  `json:"projectId"`
		TaskID              string `json:"taskId"`
		VersionID           string `json:"versionId"`
		VersionLabel        string `json:"versionLabel"`
		Repository          string `json:"repository"`
		Ref                 string `json:"ref"`
		CommitSHA           string `json:"commitSha"`
		TreeSHA             string `json:"treeSha"`
		PinRevision         int64  `json:"pinRevision"`
		PinReplayed         *bool  `json:"pinReplayed"`
		PublicationReplayed *bool  `json:"publicationReplayed"`
		DiscoveryRunID      string `json:"discoveryRunId"`
		DiscoveryState      string `json:"discoveryState"`
	}
	var label string
	project, err := strconv.ParseInt(params["projectId"], 10, 64)
	if err != nil || project <= 0 || !boardOpaque.MatchString(params["taskId"]) || json.Unmarshal(input["versionLabel"], &label) != nil || json.Unmarshal(body, &receipt) != nil {
		return fail()
	}
	if receipt.ProjectID != project || receipt.TaskID != params["taskId"] || receipt.VersionLabel != label || !passostate.ValidVersionLabel(label) || !boardUUID.MatchString(receipt.VersionID) || !boardUUID.MatchString(receipt.DiscoveryRunID) || !publishedGitSHA.MatchString(receipt.CommitSHA) || !publishedGitSHA.MatchString(receipt.TreeSHA) || receipt.PinRevision <= 0 || receipt.PinReplayed == nil || receipt.PublicationReplayed == nil || !publishedRepository.MatchString(receipt.Repository) || !strings.HasPrefix(receipt.Ref, "refs/heads/") || len(receipt.Ref) <= len("refs/heads/") || strings.HasSuffix(receipt.Ref, "/") || strings.HasSuffix(receipt.Ref, ".") || strings.Contains(receipt.Ref, "//") || strings.Contains(receipt.Ref, "..") || strings.ContainsAny(receipt.Ref, "\r\n\x00 ~^:?*[\\") {
		return fail()
	}
	switch receipt.DiscoveryState {
	case "queued", "running", "succeeded", "failed", "cancelled", "stale":
	default:
		return fail()
	}
	return nil
}
