Download the file for your computer, open it, and click **Turn on uploads**. The app copies itself to its permanent location on first launch, then shares new WoWCensus data whenever you log out or `/reload`.

| Your computer | Download |
|---|---|
| Windows | `wowcensus-companion.exe` |
| Mac | `WoWCensus-Companion-VERSION-macos.zip` |
| Linux | `WoWCensus-Companion-VERSION-linux-x86_64.AppImage` |

The `SHA256SUMS` files let the app verify its own updates; you can ignore them.

**Windows:** if you see "Windows protected your PC", click **More info**, then **Run anyway**. It installs in `C:\WoWCensus`.

**Mac:** unzip, then open **WoWCensus Companion**. It copies itself to `~/Applications` and opens the installed copy. The first time you open it, macOS may say it can't check the app. Go to **System Settings → Privacy & Security** and click **Open Anyway**.

**Linux:** make the AppImage executable (`chmod +x`) and run it. It copies itself to `~/.local/opt/wowcensus-companion` and adds an application-menu entry.
