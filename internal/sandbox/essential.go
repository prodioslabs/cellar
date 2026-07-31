package sandbox

import "strings"

// EssentialHosts is the curated allowlist of development-essential domains
// (package registries, git hosts, container registries, major AI APIs, CDNs).
// Matched with the same rules as NetworkRule host patterns: exact, subdomain,
// or *.wildcard.
var EssentialHosts = []string{
	// NPM / Yarn / Bun
	"registry.npmjs.org",
	"registry.npmjs.com",
	"nodejs.org",
	"nodesource.com",
	"deb.nodesource.com",
	"npm.pkg.github.com",
	"yarnpkg.com",
	"*.yarnpkg.com",
	"yarn.npmjs.org",
	"yarnpkg.netlify.com",
	"bun.sh",
	"*.bun.sh",
	// Nix
	"cache.nixos.org",
	"channels.nixos.org",
	"releases.nixos.org",
	// Git
	"github.com",
	"*.github.com",
	"*.githubusercontent.com",
	"gh.io",
	"ghcr.io",
	"gitlab.com",
	"*.gitlab.com",
	"bitbucket.org",
	// Python
	"pypi.org",
	"pypi.python.org",
	"files.pythonhosted.org",
	"bootstrap.pypa.io",
	"astral.sh",
	"*.astral.sh",
	"repo.anaconda.com",
	// Rust
	"crates.io",
	"static.crates.io",
	"index.crates.io",
	"static.rust-lang.org",
	"rustup.rs",
	"sh.rustup.rs",
	"doc.rust-lang.org",
	// Go
	"proxy.golang.org",
	"sum.golang.org",
	"index.golang.org",
	"go.dev",
	"golang.org",
	"*.golang.org",
	// CMake
	"cmake.org",
	// Composer / NuGet / Hex / RubyGems
	"packagist.org",
	"*.packagist.org",
	"packagist.com",
	"nuget.org",
	"*.nuget.org",
	"hex.pm",
	"*.hex.pm",
	"rubygems.org",
	"*.rubygems.org",
	// apt
	"*.ubuntu.com",
	"*.debian.org",
	"cdn-fastly.deb.debian.org",
	// CDN
	"unpkg.com",
	"jsdelivr.net",
	"fastly.com",
	"cloudflare.com",
	// AI / ML
	"*.anthropic.com",
	"claude.ai",
	"*.claude.ai",
	"platform.claude.com",
	"openai.com",
	"*.openai.com",
	"chatgpt.com",
	"generativelanguage.googleapis.com",
	"gemini.google.com",
	"aistudio.google.com",
	"ai.google.dev",
	"models.dev",
	"api.perplexity.ai",
	"api.deepseek.com",
	"api.groq.com",
	"openrouter.ai",
	"cursor.com",
	"*.cursor.com",
	"*.cursor.sh",
	"huggingface.co",
	"*.huggingface.co",
	"hf.co",
	"*.hf.co",
	// Container registries
	"docker.io",
	"*.docker.io",
	"*.docker.com",
	"mcr.microsoft.com",
	"registry.k8s.io",
	"gcr.io",
	"*.gcr.io",
	"*.pkg.dev",
	"registry.cloud.google.com",
	"quay.io",
	"public.ecr.aws",
	"*.ecr.aws",
	// Maven
	"repo1.maven.org",
	"repo.maven.apache.org",
	// Google Fonts
	"fonts.googleapis.com",
	"fonts.gstatic.com",
}

// IsEssentialHost reports whether host matches the essential-services allowlist.
func IsEssentialHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false
	}
	for _, p := range EssentialHosts {
		if essentialNameMatch(host, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func essentialNameMatch(host, pattern string) bool {
	switch {
	case pattern == "":
		return false
	case strings.HasPrefix(pattern, "*."):
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix) || host == suffix[1:]
	case strings.HasPrefix(pattern, "."):
		return strings.HasSuffix(host, pattern) || host == pattern[1:]
	default:
		return host == pattern || strings.HasSuffix(host, "."+pattern)
	}
}
