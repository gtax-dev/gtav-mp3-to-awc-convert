# gtav-mp3-to-awc-convert

Turn MP3 files into GTA V sounds from the command line. Each MP3 becomes a ready-to-run
**FiveM resource** (zip) or a **singleplayer `dlc.rpf`**, with the sound bank (`.awc`) and its sound
metadata already set up.

The conversion runs on the [gtax.dev](https://gtax.dev) MP3 to AWC API, so you need a free gtax.dev
API key. Get one at [gtax.dev/api-keys](https://gtax.dev/api-keys).

## Install

Download a binary for Windows, macOS or Linux from the
[releases page](https://github.com/gtax-dev/gtav-mp3-to-awc-convert/releases), or build it with Go 1.21+:

```sh
go install github.com/gtax-dev/gtav-mp3-to-awc-convert@latest
```

## Usage

```sh
export GTAX_API_KEY=gtax_dglb_...        # PowerShell: $env:GTAX_API_KEY = 'gtax_dglb_...'

gtav-mp3-to-awc-convert horn.mp3                     # -> horn_sfx.zip (FiveM resource)
gtav-mp3-to-awc-convert -format dlc-rpf horn.mp3     # -> horn/dlc.rpf (singleplayer)
gtav-mp3-to-awc-convert -start 2 -length 5 song.mp3  # keep 5 seconds starting at 0:02
gtav-mp3-to-awc-convert sounds/*.mp3                 # one pack per file
```

| Option | Default | |
|---|---|---|
| `-format` | `fivem-zip` | `fivem-zip` (FiveM resource) or `dlc-rpf` (singleplayer) |
| `-project` | file name | Project name. Letters, digits and `_` are kept, lowercased. |
| `-bank` | project name | Sound bank name |
| `-sound` | bank name | Name of the sound in game |
| `-start` | `0` | Start of the part to keep, in seconds |
| `-length` | `0` | Length to keep, in seconds. `0` keeps everything after `-start`. |
| `-o` | see below | Output path (one input file only) |
| `-api-key` | `$GTAX_API_KEY` | Your gtax.dev API key |
| `-quiet` | | Only print the written file paths |

By default a FiveM pack is written as `<project>_sfx.zip` and a singleplayer pack as
`<project>/dlc.rpf`, the folder layout the game expects.

**Keep sounds short.** A sound bank has to fit in one in-game slot of about 4 MB, which is roughly
40 seconds of audio at 48 kHz. Use `-start` and `-length` to cut longer files. Uploads are limited
to 8 MB per file.

## Using the sound

**FiveM:** unzip the pack into your server's `resources/` folder, add `ensure <project>_sfx` to
`server.cfg` and restart. The pack includes test commands:

- `/playsound2d` plays it as a UI sound
- `/playsound3d` plays it on your character
- `/playsoundvehicle` plays it on the vehicle you're in
- `/sfxbank` checks that the bank loads, `/releasesfx` unloads it

The `README.md` inside the zip shows the exact names to use in your own scripts.

**Singleplayer:** copy the `<project>` folder into `mods/update/x64/dlcpacks/` and add
`<Item>dlcpacks:/<project>/</Item>` to `dlclist.xml`.

## Limits

Daily conversions depend on your gtax.dev plan: Free and Basic 10, Pro 200, Ultra unlimited. The tool
prints how many you have left after each file.

## License

MIT
