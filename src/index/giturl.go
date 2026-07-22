package index

// Repo URL normalization, adapted from BuildBuddy's server/util/git (MIT
// licensed):
// https://github.com/buildbuddy-io/buildbuddy/blob/master/server/util/git/git.go
//
// Trimmed to just URL parsing/normalization: the original file's
// credential-injection and GitHub-specific helpers depend on BuildBuddy's
// internal flag/log/status packages and aren't needed here.

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

var (
	schemeRegexp                         = regexp.MustCompile(`^(([a-z0-9+.-]+:)?/)?/`)
	missingSlashBetweenPortAndPathRegexp = regexp.MustCompile(`^(([a-z0-9+.-]+:)?//([0-9a-zA-Z%._~-]+(:[0-9a-zA-Z%._~-]*)?@)?[0-9a-zA-Z%._~-]+:[0-9]*[^0-9/@])[^@]*$`)
)

func parseRepoURL(repo string) (*url.URL, error) {
	repo = strings.TrimSpace(repo)
	// We assume https:// when scheme is missing for all domains except localhost,
	// which in most cases uses http:// since most people forgo the hassle of
	// setting up HTTPS locally. We assume "github.com" if no scheme or domain is
	// specified and the path is relative. We strip any trailing slash.

	if schemeRegexp.FindStringIndex(repo) == nil {
		// convert e.g. user@host:port/path/to/repo -> //user@host:port/path/to/repo
		repo = "//" + repo
	}
	if matches := missingSlashBetweenPortAndPathRegexp.FindStringSubmatchIndex(repo); matches != nil {
		// convert e.g. //user@host:path/to/repo -> //user@host:/path/to/repo
		repo = repo[:matches[3]-1] + "/" + repo[matches[3]-1:]
	}

	repoURL, err := url.Parse(repo)
	if err != nil {
		return nil, err
	}

	if repoURL.Path != "/" {
		repoURL.Path = strings.TrimSuffix(repoURL.Path, "/")
	}

	// convert e.g file://buildbuddy-io/buildbuddy -> buildbuddy-io/buildbuddy
	// and e.g //buildbuddy-io/buildbuddy -> buildbuddy-io/buildbuddy
	if (repoURL.Scheme == "file" || repoURL.Scheme == "") && repoURL.Host != "" && repoURL.Hostname() != "localhost" && !strings.ContainsAny(repoURL.Host, ".:") && repoURL.Path != "" {
		repoURL.Scheme = ""
		repoURL.Path = repoURL.Host + repoURL.Path
		repoURL.Host = ""
	}

	if repoURL.Scheme == "" && repoURL.Host == "" && !strings.HasPrefix(repoURL.Path, "/") {
		if host, _, found := strings.Cut(repoURL.Path, "/"); strings.ContainsAny(host, ".:") || host == "localhost" {
			// convert e.g gitlab.com/buildbuddy-io/buildbuddy -> //gitlab.com/buildbuddy-io/buildbuddy
			repoURL.Host = host
			repoURL.Path = repoURL.Path[len(host):]
		} else if found {
			// convert e.g buildbuddy-io/buildbuddy -> //github.com/buildbuddy-io/buildbuddy
			repoURL.Host = "github.com"
			repoURL.Path = "/" + repoURL.Path
		}
	}

	// strip trailing ":" on hosts that lack a port.
	host, port, err := net.SplitHostPort(repoURL.Host)
	if err == nil && port == "" {
		repoURL.Host = strings.TrimSuffix(net.JoinHostPort(host, port), ":")
	}

	// assume missing scheme
	if repoURL.String() != "" && repoURL.Scheme == "" {
		if repoURL.Hostname() == "localhost" {
			// assume http for missing localhost scheme.
			repoURL.Scheme = "http"
		} else if repoURL.Host == "" {
			// assume file for missing empty host scheme.
			repoURL.Scheme = "file"
		} else if repoURL.User.Username() != "" {
			// assume ssh for missing scheme with user specified.
			repoURL.Scheme = "ssh"
		} else {
			// assume https for all other missing scheme cases.
			repoURL.Scheme = "https"
		}
	}

	return repoURL, nil
}

// normalizeRepoURL coerces https scheme for all domains except localhost,
// strips user info, and strips the ".git" suffix.
func normalizeRepoURL(repo string) (*url.URL, error) {
	repo = strings.TrimSpace(repo)
	repoURL, err := parseRepoURL(repo)
	if err != nil {
		return nil, err
	}

	if repoURL.Scheme == "file" || (repoURL.Scheme == "" && repoURL.Hostname() == "" && repoURL.Path != "") {
		repoURL.Scheme = "file"
	} else if repoURL.Scheme != "https" && repoURL.Hostname() == "localhost" {
		repoURL.Scheme = "http"
	} else if repoURL.String() != "" {
		repoURL.Scheme = "https"
	}

	repoURL.User = nil
	repoURL.Path = strings.TrimSuffix(repoURL.Path, ".git")

	return repoURL, nil
}
