# Security policy

## Reporting a vulnerability

Please report security problems **privately** through GitHub security advisories: open the repository's **Security** tab and choose **Report a vulnerability**. Do not open a public issue or pull request for a vulnerability.

Include what you found, how to reproduce it, the version (`datacodex-notify-cli version`), and the impact you expect. You will get an acknowledgement and a plan as soon as a maintainer can look at it.

## What is in scope

- The local HTTP API on `127.0.0.1` (authentication, the Host and Origin checks, the CSRF token, the session cookie).
- Storage of the access token and Gemini keys in Windows Credential Manager, and anything that could leak them (logs, prompts, the window).
- The command-line tool and the installer.

## What is out of scope

- Account restrictions by WhatsApp. The app uses an unofficial client library (whatsmeow); see the disclaimer in the README.
- Problems that need an attacker who already controls your Windows account.
- Vulnerabilities in third-party libraries that are already fixed upstream (a pull request that updates the dependency is welcome).

## Supported versions

Only the latest release receives security fixes.
