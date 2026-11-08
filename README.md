# Fanart.tv Artwork for Silo

Movie and series posters, backgrounds, logos, clear art, banners, thumbs, discs and season posters from [Fanart.tv](https://fanart.tv). Connects directly to Fanart.tv; no separate container is needed.

## Install

1. Open Silo's administrator **Plugins** page, select **Catalog**, and click **Add repository**. Give it a name such as **Fanart.tv** and use this URL:

   ```text
   https://raw.githubusercontent.com/Joloxx9/silo-plugin-fanart/spark/index.json
   ```

2. In **Catalog**, find **Fanart.tv Artwork**, click **Install**, then enable it.

Available for Linux amd64, Linux arm64 and macOS arm64.

## Setup

1. Copy the personal API key from your fanart.tv profile and save it in the plugin settings as **Fanart.tv API key**.
2. Add **Fanart.tv Artwork** to the movie, series and season provider lists. Put it first for the types you want from Fanart.tv, or after another artwork provider (TMDB, MediUX) to fill gaps such as clear art, discs and banners.
3. Keep a metadata provider for titles and IDs. Movies are looked up by TMDB or IMDb ID, series by TVDB ID.
4. Open **Fanart.tv status** in the admin menu to confirm the key is saved and accepted. In the settings form the key field stays empty after saving; leave it empty to keep the saved key.
5. Refresh an item to apply. Saving settings does not replace existing artwork.

## Settings

- **Use for** chooses movies, series (with seasons) or both. Default: movies only.
- **Artwork types** limits what the plugin supplies.
- **Best image only** supplies one image per type on refresh; the manual picker still shows every image. **All matching images** supplies every image.
- **Cache hours** controls how long Fanart.tv lookups are reused. Default: 24. Set 0 to disable.

## Selection

Silo sends the library's metadata language with each request. Within each type, images are ranked by language, then HD, then Fanart.tv likes. Posters prefer the library language, then English, then other languages, then textless art. Logos, clear art, banners, thumbs and discs prefer the library language, then English, then textless art, then other languages. Backgrounds prefer textless art. Silo then picks across providers by the same language order.

HD logos and HD clear art rank ahead of the older low-resolution versions. Fanart.tv has no episode artwork.

## Notes

References are stored as `fanart://<path>` and resolved to `https://assets.fanart.tv/fanart/<path>`; the API key is never part of an image URL. Lookups have timeouts, size limits and limited concurrency, and duplicate in-flight lookups are shared.

Structure and release tooling follow [silo-plugin-mediux](https://github.com/Pukabyte/silo-plugin-mediux).

[AGPL-3.0-or-later](LICENSE). Independently maintained; not affiliated with Fanart.tv or Silo.
