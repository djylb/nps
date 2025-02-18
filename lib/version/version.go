package version

var VERSION = "0.26.36"

// Compulsory minimum version, Minimum downward compatibility to this version
func GetVersion() string {
	return VERSION
}
