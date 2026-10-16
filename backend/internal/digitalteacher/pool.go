package digitalteacher

import "regexp"

var callerScopePattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Caller identities are accepted only from explicitly registered organization
// gateway keys. Ordinary personal keys cannot rotate scopes to evade quotas.
// These are service connections, not an end-user allowlist.
func (g *Gateway) Caller(o Owner, scope string) (Owner, error) {
	delegated := g.organizationKeys[o.KeyID]
	if scope == "" && !delegated {
		return o, nil
	}
	if !delegated || !callerScopePattern.MatchString(scope) {
		return Owner{}, fail(403, "digital_teacher_caller_invalid", "当前组织数字老师连接未完成身份校验")
	}
	o.Scope = scope
	return o, nil
}

func (g *Gateway) callerEnabled(o Owner) bool {
	return o.Scope == "" || g.poolFor(o) && g.organizationKeys[o.KeyID]
}

func (g *Gateway) poolFor(o Owner) bool { return g.anamPool && g.anamPoolGroups[o.GroupID] }
