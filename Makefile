.PHONY: runtime-release-check runtime-release-pr publish-runtime release publish-release

runtime-release-check:
	@command -v gh >/dev/null 2>&1 || { echo "gh is required"; exit 1; }
	@gh auth status >/dev/null
	@gh workflow run verify.yml --ref "$$(git branch --show-current)"
	@echo "Triggered native verification and a build-and-assemble proof without opening a PR."

runtime-release-pr:
	@command -v gh >/dev/null 2>&1 || { echo "gh is required"; exit 1; }
	@gh auth status >/dev/null
	@test "$$(git branch --show-current)" = main || { echo "runtime-release-pr must be dispatched from main"; exit 1; }
	@gh workflow run release-prep.yml --ref main -f open_pr=true
	@echo "Triggered the runtime release-prep workflow; its PR records the promotion run ID."

publish-runtime:
	@command -v gh >/dev/null 2>&1 || { echo "gh is required"; exit 1; }
	@gh auth status >/dev/null
	@[ -n "$(PREP_RUN_ID)" ] || { echo "PREP_RUN_ID is required"; exit 1; }
	@gh workflow run release.yml --ref main -f prep_run_id="$(PREP_RUN_ID)"
	@echo "Triggered exact-byte runtime promotion from workflow run $(PREP_RUN_ID)."

# Compatibility names from the previous binary-in-Git release flow.
release: runtime-release-pr

publish-release: publish-runtime
