package toolpolicy

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// The helper functions are pure: no network, no clock, no files. Every one of
// them refuses (returns an error, which denies the call) rather than guessing
// when its input is out of bounds or ambiguous.

const maxHelperList = 256

func stringArg(v ref.Val, name string) (string, ref.Val) {
	s, ok := v.Value().(string)
	if !ok {
		return "", types.NewErr("%s: string argument required", name)
	}
	if len(s) > MaxHelperString {
		return "", types.NewErr("%s: string is longer than %d bytes", name, MaxHelperString)
	}
	return s, nil
}

func lowerBinding(v ref.Val) ref.Val {
	s, e := stringArg(v, "lower")
	if e != nil {
		return e
	}
	return types.String(strings.ToLower(s))
}

// pathUnder reports whether p, after lexical normalization (".", ".." and
// repeated separators are resolved), is root or lies below it. Both must be
// absolute. It does not decode percent escapes and does not resolve symlinks.
func pathUnder(p, root string) (bool, error) {
	for _, s := range []string{p, root} {
		if len(s) > 4096 || strings.ContainsAny(s, "\x00\\") {
			return false, fmt.Errorf("path_under: path is too long or contains a NUL or backslash")
		}
		if !strings.HasPrefix(s, "/") {
			return false, fmt.Errorf("path_under: absolute paths are required")
		}
	}
	p, root = path.Clean(p), path.Clean(root)
	if root == "/" {
		return true, nil
	}
	return p == root || strings.HasPrefix(p, root+"/"), nil
}

func pathUnderBinding(p, root ref.Val) ref.Val {
	a, ok1 := p.Value().(string)
	b, ok2 := root.Value().(string)
	if !ok1 || !ok2 {
		return types.NewErr("path_under: string arguments required")
	}
	under, err := pathUnder(a, b)
	if err != nil {
		return types.NewErr("%s", err.Error())
	}
	return types.Bool(under)
}

// urlHost returns the lower-case host of a strictly parsed http or https URL.
// URLs with credentials, non-ASCII or percent-encoded hosts, control
// characters, backslashes or no host are errors.
func urlHost(raw string) (string, error) {
	if len(raw) == 0 || len(raw) > 2048 {
		return "", fmt.Errorf("url_host: url is empty or too long")
	}
	for _, c := range raw {
		if c <= 0x20 || c == 0x7f || c == '\\' || c > 0x7e {
			return "", fmt.Errorf("url_host: url contains a control, space, backslash or non-ASCII character")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("url_host: not a plain http or https URL")
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(u.Host, "%") {
		return "", fmt.Errorf("url_host: url has no usable host")
	}
	return strings.TrimSuffix(strings.ToLower(host), "."), nil
}

func urlHostBinding(v ref.Val) ref.Val {
	s, ok := v.Value().(string)
	if !ok {
		return types.NewErr("url_host: string argument required")
	}
	host, err := urlHost(s)
	if err != nil {
		return types.NewErr("%s", err.Error())
	}
	return types.String(host)
}

func anyInBinding(a, b ref.Val) ref.Val {
	left, ok1 := a.(traits.Lister)
	right, ok2 := b.(traits.Lister)
	if !ok1 || !ok2 {
		return types.NewErr("any_in: list arguments required")
	}
	if n, _ := left.Size().(types.Int); n > maxHelperList {
		return types.NewErr("any_in: lists are bounded to %d entries", maxHelperList)
	}
	if n, _ := right.Size().(types.Int); n > maxHelperList {
		return types.NewErr("any_in: lists are bounded to %d entries", maxHelperList)
	}
	for i := left.Iterator(); i.HasNext() == types.True; {
		item := i.Next()
		for j := right.Iterator(); j.HasNext() == types.True; {
			if item.Equal(j.Next()) == types.True {
				return types.True
			}
		}
	}
	return types.False
}

// functions returns the environment options that declare the helpers. The
// standard matches (RE2) is kept; inspect requires its pattern to be a string
// literal of at most MaxPatternBytes.
func functions() []cel.EnvOption {
	str, dyn := cel.StringType, cel.DynType
	return []cel.EnvOption{
		cel.Function("lower", cel.Overload("lower_string", []*cel.Type{str}, str, cel.UnaryBinding(lowerBinding))),
		cel.Function("path_under", cel.Overload("path_under_string_string", []*cel.Type{str, str}, cel.BoolType, cel.BinaryBinding(pathUnderBinding))),
		cel.Function("url_host", cel.Overload("url_host_string", []*cel.Type{str}, str, cel.UnaryBinding(urlHostBinding))),
		cel.Function("any_in", cel.Overload("any_in_list_list", []*cel.Type{cel.ListType(dyn), cel.ListType(dyn)}, cel.BoolType, cel.BinaryBinding(anyInBinding))),
	}
}
