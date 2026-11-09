package launch

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/djylb/nps/lib/connprofile"
)

// Pack returns the content of the file at path as a -launch value, gzip and
// base64. The value holds every secret of the file in the clear.
func Pack(ctx context.Context, path string) (string, error) {
	text, err := readFile(path)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(text)
	if trimmed != "" && (trimmed[0] == '@' || isURL(trimmed)) {
		return "", errors.New("the file only refers to other content: pack that instead")
	}
	if _, err := parseContent(ctx, text, true, 0); err != nil {
		return "", err
	}
	var b bytes.Buffer
	z, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	_, err = z.Write([]byte(text))
	if err = errors.Join(err, z.Close()); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b.Bytes()), nil
}

// Show prints what sources and locals amount to in the current form, one
// argument per connection and access; reveal false masks vkeys and access
// keys.
func Show(ctx context.Context, sources []Source, locals []Local, reveal bool) (string, error) {
	plan, err := NewComposer(sources, locals).Load(ctx)
	if err != nil {
		return "", err
	}
	if plan.Retry > 0 {
		return "", errors.New("a remote source is not available")
	}
	parts := []string{"npc"}
	var notes []string
	multi := len(plan.Profiles) > 1
	for _, p := range plan.Profiles {
		c := p.Config.CommonConfig
		common := make(map[string]string, len(p.written))
		for _, k := range slices.Sorted(maps.Keys(p.written)) {
			common[k] = sessionValue(c, k)
		}
		vkey := ""
		if reveal {
			vkey = c.VKey
		}
		name := p.Name
		if p.auto && !multi {
			name = ""
		}
		link, err := connprofile.FormatLink(vkey, c.Server, c.Tp, name, common)
		if err != nil {
			return "", fmt.Errorf("%s: %w", p.Source, err)
		}
		parts = append(parts, `-launch="`+link+`"`)
		via := ""
		if multi {
			via = p.Name
		}
		for _, l := range p.Config.LocalServer {
			parts = append(parts, `-L="`+formatLocal(l, via, reveal)+`"`)
		}
		if n := len(p.Config.Hosts) + len(p.Config.Tasks); n > 0 {
			notes = append(notes, fmt.Sprintf("# %s also defines %d tunnels or hosts, which only a configuration file can write", p.Source, n))
		}
	}
	return strings.Join(append([]string{strings.Join(parts, " ")}, notes...), "\n"), nil
}
