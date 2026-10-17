package servercfg

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/djylb/nps/lib/common"
	viperini "github.com/go-viper/encoding/ini"
	"github.com/spf13/viper"
)

type parserFunc func(path string) (map[string]any, error)

type store struct {
	values   map[string]any
	snapshot *Snapshot
}

var (
	mu               sync.RWMutex
	appConfig        *store
	appConfigPath    string
	preferredPath    string
	parsers          = make(map[string]parserFunc)
	extensionFormats = make(map[string]string)
	extensionOrder   []string
	errConfigMissing = errors.New("server config not loaded")
)

func init() {
	mustRegisterViperFormat("ini", ".conf", ".ini")
	mustRegisterViperFormat("yaml", ".yaml", ".yml")
	mustRegisterViperFormat("json", ".json")
}

func mustRegisterViperFormat(format string, extensions ...string) {
	if err := RegisterViperFormat(format, extensions...); err != nil {
		panic(err)
	}
}

// RegisterViperFormat registers a new config format backed by Viper.
func RegisterViperFormat(format string, extensions ...string) error {
	return RegisterFormat(format, newViperParser(format), extensions...)
}

// RegisterFormat registers a parser for one or more file extensions.
func RegisterFormat(format string, parser parserFunc, extensions ...string) error {
	format = strings.TrimSpace(strings.ToLower(format))
	if format == "" {
		return fmt.Errorf("config format cannot be empty")
	}
	if parser == nil {
		return fmt.Errorf("config parser for %q cannot be nil", format)
	}
	if len(extensions) == 0 {
		return fmt.Errorf("config format %q must declare at least one extension", format)
	}

	mu.Lock()
	defer mu.Unlock()

	if _, exists := parsers[format]; exists {
		return fmt.Errorf("config format %q already registered", format)
	}
	parsers[format] = parser
	for _, ext := range extensions {
		normalized := normalizeExt(ext)
		if normalized == "" {
			return fmt.Errorf("config format %q has invalid extension %q", format, ext)
		}
		if owner, exists := extensionFormats[normalized]; exists {
			return fmt.Errorf("config extension %q already registered by %q", normalized, owner)
		}
		extensionFormats[normalized] = format
		extensionOrder = append(extensionOrder, normalized)
	}
	return nil
}

func newViperParser(format string) parserFunc {
	return func(path string) (map[string]any, error) {
		registry := viper.NewCodecRegistry()
		if err := registry.RegisterCodec("ini", viperini.Codec{KeyDelimiter: "::"}); err != nil {
			return nil, err
		}
		cfg := viper.NewWithOptions(
			viper.KeyDelimiter("::"),
			viper.WithCodecRegistry(registry),
		)
		cfg.SetConfigFile(path)
		cfg.SetConfigType(format)
		if err := cfg.ReadInConfig(); err != nil {
			return nil, err
		}
		settings := cfg.AllSettings()
		if format == "ini" {
			settings = mergeINIDefaultSection(settings)
		}
		return flattenSettings(settings), nil
	}
}

func mergeINIDefaultSection(root map[string]any) map[string]any {
	if len(root) == 0 {
		return root
	}
	merged := make(map[string]any, len(root))
	for key, value := range root {
		if normalizeKey(key) == "default" {
			switch section := value.(type) {
			case map[string]any:
				for sectionKey, sectionValue := range section {
					if _, exists := merged[sectionKey]; !exists {
						merged[sectionKey] = sectionValue
					}
				}
			case map[any]any:
				for sectionKey, sectionValue := range section {
					textKey := fmt.Sprint(sectionKey)
					if _, exists := merged[textKey]; !exists {
						merged[textKey] = sectionValue
					}
				}
			default:
				merged[key] = value
			}
			continue
		}
		merged[key] = value
	}
	return merged
}

// SetPreferredPath configures an explicit config file path to try before defaults.
func SetPreferredPath(path string) {
	mu.Lock()
	preferredPath = strings.TrimSpace(path)
	mu.Unlock()
}

// DefaultPaths returns the candidate nps server config paths in load order.
func DefaultPaths() []string {
	mu.RLock()
	explicit := preferredPath
	mu.RUnlock()

	candidates := []string{explicit}
	for _, base := range []string{common.GetRunPath(), common.GetAppPath()} {
		for _, ext := range SupportedExtensions() {
			candidates = append(candidates, filepath.Join(base, "conf", "nps"+ext))
		}
	}
	return dedupePaths(candidates...)
}

// SupportedExtensions returns all registered config file extensions.
func SupportedExtensions() []string {
	mu.RLock()
	defer mu.RUnlock()
	return slices.Clone(extensionOrder)
}

// IsSupportedConfigPath reports whether the file extension maps to a registered parser.
func IsSupportedConfigPath(path string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := extensionFormats[normalizeExt(filepath.Ext(path))]
	return ok
}

func dedupePaths(paths ...string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

func current() *store {
	mu.RLock()
	defer mu.RUnlock()
	return appConfig
}

// Path returns the path of the currently loaded server config file.
func Path() string {
	mu.RLock()
	defer mu.RUnlock()
	return appConfigPath
}

// Load loads the server config from the provided file.
func Load(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("config path cannot be empty")
	}

	parser, format, err := parserForPath(path)
	if err != nil {
		return err
	}
	values, err := parser(path)
	if err != nil {
		return fmt.Errorf("parse %s config %s: %w", format, path, err)
	}
	snapshot := buildSnapshot(values)

	mu.Lock()
	appConfig = &store{values: values, snapshot: snapshot}
	appConfigPath = path
	mu.Unlock()
	currentSnapshot.Store(snapshot)
	return nil
}

// LoadCandidates loads the first readable config file from the provided paths.
func LoadCandidates(paths ...string) error {
	paths = dedupePaths(paths...)
	if len(paths) == 0 {
		return fmt.Errorf("no server config path provided")
	}
	var errs []error
	for _, path := range paths {
		if err := Load(path); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Errorf("load %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

// LoadDefault loads the server config from the default candidate paths.
func LoadDefault() error {
	return LoadCandidates(DefaultPaths()...)
}

// Reload reloads the currently loaded config file. If no config has been loaded
// yet, it falls back to the default candidate paths.
func Reload() error {
	if path := Path(); path != "" {
		return Load(path)
	}
	return LoadDefault()
}

func parserForPath(path string) (parserFunc, string, error) {
	ext := normalizeExt(filepath.Ext(path))

	mu.RLock()
	format, ok := extensionFormats[ext]
	parser := parsers[format]
	mu.RUnlock()

	if ok && parser != nil {
		return parser, format, nil
	}
	if ext == "" {
		return nil, "", fmt.Errorf("config path %s has no extension", path)
	}
	return nil, "", fmt.Errorf("unsupported config extension %q for %s", ext, path)
}

func normalizeExt(ext string) string {
	ext = strings.TrimSpace(strings.ToLower(ext))
	if ext == "" {
		return ""
	}
	if strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}

func normalizeKey(key string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(strings.ToLower(key)), func(r rune) bool {
		return r == '_' || r == '.' || r == '-' || unicode.IsSpace(r)
	})
	return strings.Join(parts, "_")
}

func joinKey(parts ...string) string {
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if normalized := normalizeKey(part); normalized != "" {
			items = append(items, normalized)
		}
	}
	return strings.Join(items, "_")
}

func getValue(name string) (any, error) {
	cfg := current()
	if cfg == nil {
		return nil, errConfigMissing
	}
	return lookupValue(cfg.values, name)
}

func lookupValue(values map[string]any, name string) (any, error) {
	if values == nil {
		return nil, errConfigMissing
	}
	normalized := normalizeKey(name)
	if normalized == "" {
		return nil, fmt.Errorf("config key cannot be empty")
	}
	value, ok := values[normalized]
	if !ok {
		return nil, fmt.Errorf("config key %q not found", name)
	}
	return value, nil
}

func String(key string) string {
	value, err := getValue(key)
	if err != nil {
		return ""
	}
	return stringifyValue(value)
}

func DefaultString(key, defaultValue string) string {
	if value := String(key); value != "" {
		return value
	}
	return defaultValue
}

func Bool(key string) (bool, error) {
	value, err := getValue(key)
	if err != nil {
		return false, err
	}
	return toBool(value)
}

func Int(key string) (int, error) {
	value, err := getValue(key)
	if err != nil {
		return 0, err
	}
	return toInt(value)
}

func Int64(key string) (int64, error) {
	value, err := getValue(key)
	if err != nil {
		return 0, err
	}
	return toInt64(value)
}

func flattenSettings(root map[string]any) map[string]any {
	values := make(map[string]any)
	for key, value := range root {
		flattenInto(normalizeKey(key), value, values)
	}
	return values
}

func flattenInto(prefix string, value any, out map[string]any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			flattenInto(joinKey(prefix, key), item, out)
		}
	case map[any]any:
		for key, item := range v {
			flattenInto(joinKey(prefix, fmt.Sprint(key)), item, out)
		}
	default:
		if prefix != "" {
			out[prefix] = value
		}
	}
}

func stringifyValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case []string:
		return strings.Join(v, ",")
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, stringifyValue(item))
		}
		return strings.Join(items, ",")
	default:
		return fmt.Sprint(v)
	}
}

func toBool(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(strings.TrimSpace(v))
	default:
		return strconv.ParseBool(stringifyValue(v))
	}
}

func toInt(value any) (int, error) {
	v, err := toInt64(value)
	return int(v), err
}

func toInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v > math.MaxInt64 {
			return 0, fmt.Errorf("value %d overflows int64", v)
		}
		return int64(v), nil
	case float32:
		if math.Trunc(float64(v)) != float64(v) {
			return 0, fmt.Errorf("value %v is not an integer", v)
		}
		return int64(v), nil
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("value %v is not an integer", v)
		}
		return int64(v), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	default:
		return strconv.ParseInt(stringifyValue(v), 10, 64)
	}
}
