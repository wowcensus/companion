# WoWCensus Companion

A small app that sends your WoWCensus add-on data to [wowcensus.io](https://wowcensus.io), so the site's population numbers include what you've seen in game.

You play as normal. The app runs quietly in the background and shares new data each time you log out or `/reload`.

## Before you start

You need the **WoWCensus add-on** installed and enabled in World of Warcraft. Get it from [wowcensus/addon](https://github.com/wowcensus/addon).

## Install

1. Download the latest version from the [Releases](https://github.com/wowcensus/companion/releases) page.
2. Run it. It finds your World of Warcraft folders on its own.
3. Click **Turn on uploads**.

You don't need an account, a password or a token.

## Using it

- Play WoW with the add-on enabled. Whenever you log out or type `/reload`, the app notices the new data and sends it.
- Closing the window keeps the app running in the system tray. To quit, right-click the tray icon and choose **Quit**.
- To start it automatically, turn on **Start at login** in Settings.

## What gets shared

**Shared:**
- what the WoWCensus add-on recorded: character names, realms, races, classes and levels it saw through `/who`.

**Never shared:**
- your Battle.net account or login;
- your WoW account folder name;
- file paths on your computer;
- anything from other add-ons.

Uploads are off until you turn them on, and you can turn them off at any time in Settings.

## Something not working?

- **"No WoWCensus.lua found yet"**: log in to a character with the add-on enabled, then `/reload` once. If WoW is installed somewhere unusual, add its folder in Settings.
- **Uploads failing**: the app retries on its own. The **Activity** tab shows what happened.

## Uninstall

Quit the app from the tray and delete it. To remove its settings too, delete the `WoWCensusCompanion` folder in `%AppData%` on Windows, `~/Library/Application Support` on macOS, or `~/.config` on Linux.
