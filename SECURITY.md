# Security policy

Use GitHub's private vulnerability reporting on the repository Security tab to report vulnerabilities. Do not include API keys or private Silo configuration in public issues.

The latest release is supported. CI runs the pinned `govulncheck` tool before release. The plugin contacts `webservice.fanart.tv` for artwork listings and `assets.fanart.tv` for images. API keys remain in Silo's encrypted plugin configuration and are never placed in image URLs or error messages.
