# Background assets

`web/aurora-background.png` is an original dashboard background created with the built-in ImageGen tool. It ships inside this source package and is referenced locally by the Aurora theme. The generated original was copied without editing.

Prompt: “A beautiful quiet arctic landscape at night, emerald and blue aurora borealis above distant mountain silhouettes and a still lake, rich subtle digital painting, premium atmospheric lighting, lots of dark negative space in the center for readable interface cards. Wide landscape 16:9, low contrast mountains, nuanced star field. Nature only, no people, no text, no logos, no watermark, no UI.”

Requests for Batman and Spider-Man character wallpapers were rejected by the image service. No character picture is included or represented as generated. The corresponding color themes are charcoal/gold and red/blue, with CSS backgrounds and optional browser-local user images.

Country flags: flag-icons 7.5.0, locally vendored in `web/flags/`. MIT license is preserved as `web/FLAG-LICENSE.txt`. These are exact flag assets rather than generated illustrations.

Telegram QR cards are generated deterministically in `telegram-qr.go` from actual connection links. They keep standard modules and quiet zones, with decoration outside the QR region. No generated bitmap is used to form QR codes.
