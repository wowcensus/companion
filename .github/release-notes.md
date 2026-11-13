Download the installer for your computer, run it, and click **Turn on uploads**. On Windows it installs in `C:\WoWCensus`; on macOS and Linux it installs under your user profile. It then shares new WoWCensus data whenever you log out or `/reload`.

| Your computer | Download |
|---|---|
| Windows | `WoWCensus-Companion-VERSION-windows-setup.exe` (the versioned `-windows.exe` is retained for automatic updates from older releases) |
| Mac | `WoWCensus-Companion-VERSION-macos.dmg` (or the `.zip`) |
| Linux | `WoWCensus-Companion-VERSION-linux-x86_64.AppImage` (or the `.tar.gz`, or the plain binary) |

**Windows:** use the Setup download for the `C:\WoWCensus` installation. Existing installs under AppData keep working; run Setup once to move to the new location. If you see "Windows protected your PC", click **More info**, then **Run anyway**.

**Mac:** open the `.dmg` and launch **WoWCensus Companion**. It copies itself to `~/Applications` and opens the installed copy. The first time you open it, macOS may say it can't check the app. Go to **System Settings → Privacy & Security** and click **Open Anyway**.

If version 0.1.8 was repeatedly opening or showed blank icons on Mac, quit it before opening this version from the `.dmg`. This version replaces the older copy in `~/Applications`.

**Linux:** make the AppImage executable (`chmod +x`) and run it. It copies itself to `~/.local/opt/wowcensus-companion` and adds an application-menu entry.
