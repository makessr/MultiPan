package installation

import "errors"

// Scope records ownership, never grants permissions. An omitted legacy scope
// means current-user only; it must never be inferred from the caller's token.
type Scope string

const (
	ScopeUser    Scope = "current-user"
	ScopeMachine Scope = "machine"
)

func (s Scope) explicit() bool { return s == ScopeUser || s == ScopeMachine }
func (s Scope) valid() bool    { return s == "" || s.explicit() }
func (s Scope) effective() Scope {
	if s == "" {
		return ScopeUser
	}
	return s
}

func readMarkerForScope(dir string, scope Scope) (Manifest, error) {
	m, err := readMarker(dir)
	if err == nil && (!scope.valid() || m.Scope.effective() != scope.effective()) {
		err = errors.New("安装标识与操作范围不一致，未改动文件")
	}
	return m, err
}
