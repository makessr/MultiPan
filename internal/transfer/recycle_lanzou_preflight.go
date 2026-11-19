package transfer

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type lanzouRecycleOperation string

const (
	lanzouRestore lanzouRecycleOperation = "restore"
	lanzouPurge   lanzouRecycleOperation = "delete_complete"
)

var (
	lanzouRecycleForm = regexp.MustCompile(`(?is)<form\b([^>]*)>(.*?)</form\s*>`)
	lanzouFormHash    = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)
)

// This only checks whether the backend shows a confirmation form for this
// exact operation and item. The form hash is kept local and discarded.
func checkLanzouRecycleConfirmation(ctx context.Context, client *http.Client, a Account, displayID string, operation lanzouRecycleOperation) error {
	_, err := readLanzouRecycleConfirmation(ctx, client, a, displayID, operation)
	return err
}

func readLanzouRecycleConfirmation(ctx context.Context, client *http.Client, a Account, displayID string, operation lanzouRecycleOperation) (url.Values, error) {
	kind, id, ok := strings.Cut(displayID, ":")
	if !ok || !validLanzouRecycleID(id) || kind != "file" && kind != "folder" {
		return nil, errors.New("蓝奏回收站项目不明确")
	}
	if operation != lanzouRestore && operation != lanzouPurge {
		return nil, errors.New("蓝奏回收站操作不受支持")
	}
	idKey, action := kind+"_id", kind+"_"+string(operation)
	query := url.Values{"item": {"recycle"}, "action": {action}, idKey: {id}}
	body, err := readLanzouRecyclePage(ctx, client, a, query)
	if err != nil {
		return nil, err
	}
	hash, err := parseLanzouRecycleConfirmation(body, action, idKey, id)
	if err != nil {
		return nil, err
	}
	return url.Values{"action": {action}, "task": {action}, idKey: {id}, "formhash": {hash}}, nil
}

func validateLanzouRecycleConfirmation(body, operation, idKey, id string) error {
	_, err := parseLanzouRecycleConfirmation(body, operation, idKey, id)
	return err
}

func parseLanzouRecycleConfirmation(body, operation, idKey, id string) (string, error) {
	body = lanzouRecycleHidden.ReplaceAllString(body, "")
	formats := lanzouRecycleForm.FindAllStringSubmatch(body, -1)
	bad := errors.New("蓝奏未返回与该项目及操作匹配的回收站确认表单")
	hash := ""
	for _, form := range formats {
		values := map[string]string{}
		for _, input := range lanzouRecycleInput.FindAllString(form[2], -1) {
			a := lanzouRecycleAttributes(input)
			if a == nil {
				return "", bad
			}
			if !strings.EqualFold(a["type"], "hidden") {
				continue
			}
			key := strings.ToLower(a["name"])
			switch key {
			case "formhash", "action", "task", "file_id", "folder_id":
				if _, exists := values[key]; exists {
					return "", bad
				}
				values[key] = a["value"]
			}
		}
		if _, hasProof := values["formhash"]; !hasProof {
			continue
		}
		attrs := lanzouRecycleAttributes(form[1])
		if hash != "" || !strings.EqualFold(attrs["method"], http.MethodPost) || !isLanzouRecycleFormTarget(attrs["action"]) ||
			values["action"] != operation || values["task"] != operation || values[idKey] != id ||
			!lanzouFormHash.MatchString(values["formhash"]) {
			return "", bad
		}
		for _, candidate := range []string{"file_id", "folder_id"} {
			if _, present := values[candidate]; present && candidate != idKey {
				return "", bad
			}
		}
		hash = values["formhash"]
	}
	if hash == "" {
		return "", bad
	}
	return hash, nil
}

func isLanzouRecycleFormTarget(raw string) bool {
	target, err := url.Parse(raw)
	if err != nil || target.User != nil || target.Fragment != "" || target.Opaque != "" {
		return false
	}
	base, _ := url.Parse(lanzouRecycleOrigin + "/mydisk.php")
	target = base.ResolveReference(target)
	q, err := url.ParseQuery(target.RawQuery)
	return err == nil && target.Scheme == base.Scheme && target.Host == base.Host &&
		target.EscapedPath() == base.Path && len(q) == 1 && len(q["item"]) == 1 && q.Get("item") == "recycle"
}
