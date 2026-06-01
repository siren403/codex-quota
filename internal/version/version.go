package version

import (
	"runtime/debug"
	"strings"
)

var buildVersion string

func Current() string {
	suffix := forkSuffix()

	if version := normalize(buildVersion); version != "" {
		return version + suffix
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if version := normalize(info.Main.Version); version != "" {
			return version + suffix
		}
	}

	return "dev" + suffix
}

func forkSuffix() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if strings.Contains(info.Main.Path, "siren403") {
		return "-siren403"
	}
	return ""
}

func normalize(raw string) string {
	version := strings.TrimSpace(raw)
	if version == "" || version == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(version, "v")
}
