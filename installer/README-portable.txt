DataCodex Notify - portable version
===================================

This zip holds the two programs, with no installer:

  datacodex-notify.exe       the tray app (start this one)
  datacodex-notify-cli.exe   the command-line tool agents call

How to use it
-------------
1. Unzip the folder anywhere you like (for example %LOCALAPPDATA%\Programs\DatacodexNotify).
2. Start datacodex-notify.exe. It lives in the tray; open its window from the tray icon.
3. Link WhatsApp from the Home screen and add your number (see README.md in the repository).
4. Agents must call the CLI by its full path, so keep the folder where you unzipped it.
   The Agents tab generates the prompt that tells an agent the right path.

Notes
-----
- Your data (settings, WhatsApp link, message log) is kept in %LOCALAPPDATA%\DatacodexNotify,
  not in this folder. Deleting this folder does not delete it.
- "Start with Windows" can be switched on in Settings, General. The installer is not needed for it.
- To install the agent skill for Claude Code and Codex, run:
      datacodex-notify-cli.exe skill install
  and to remove it:
      datacodex-notify-cli.exe skill remove
- Unsigned build: Windows SmartScreen may warn on first start. Choose "More info", then
  "Run anyway", after checking the SHA256 in SHA256SUMS from the release page.

License: GNU General Public License, version 3 only (see LICENSE). Source code: see the project repository. Third-party licenses: THIRD_PARTY_NOTICES.md.
This is an unofficial WhatsApp client; use a secondary number.
