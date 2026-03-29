.PHONY: release publish-release

release:
	@command -v gh >/dev/null 2>&1 || { echo "gh is required"; exit 1; }
	@gh auth status >/dev/null
	@next_version="$$( \
		if [ -n "$(VERSION)" ]; then \
			echo "$(VERSION)"; \
		else \
			git fetch --tags origin >/dev/null 2>&1; \
			latest_tag="$$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -1)"; \
			if [ -z "$$latest_tag" ]; then \
				echo v0.0.1; \
			else \
				printf '%s\n' "$$latest_tag" | \
					awk -F. '{ sub(/^v/, "", $$1); printf "v%s.%s.%d\n", $$1, $$2, $$3 + 1 }'; \
			fi; \
		fi \
	)"; \
	case "$$next_version" in \
		v*) ;; \
		*) echo "VERSION must start with v, for example v0.0.9"; exit 1 ;; \
	esac; \
	gh workflow run release-prep.yml --ref main -f version="$$next_version"; \
	echo "Triggered release-prep workflow for $$next_version."; \
	echo "After the PR merges, run: make publish-release VERSION=$$next_version"
	@echo "Watch with: gh run list --workflow=release-prep.yml --limit 1"

publish-release:
	@command -v gh >/dev/null 2>&1 || { echo "gh is required"; exit 1; }
	@gh auth status >/dev/null
	@[ -n "$(VERSION)" ] || { echo "VERSION is required, for example make publish-release VERSION=v0.0.14"; exit 1; }
	@case "$(VERSION)" in \
		v*) ;; \
		*) echo "VERSION must start with v, for example v0.0.14"; exit 1 ;; \
	esac
	@gh workflow run release.yml --ref main -f version="$(VERSION)"
	@echo "Triggered release workflow for $(VERSION)."
	@echo "Watch with: gh run list --workflow=release.yml --limit 1"
