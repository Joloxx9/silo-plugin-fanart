# Changelog

## 0.1.6

- Declare `lookup_images`, the key upstream Silo uses to ask an artwork plugin for images by TMDB, IMDb or TVDB ID. Without it, current Silo builds never call Fanart.tv for images. `image_lookup_provider_ids` stays for older builds.

## 0.1.5

- **Fanart.tv status** page in the admin menu: shows whether the API key is saved and whether Fanart.tv accepts it.

## 0.1.4

- Remove the language and fallback language settings: the library language Silo sends decides, as in 0.1.0.

## 0.1.3

- Fix: every image failed to cache ("image resolver returned empty URL") because Silo passes the path without the fanart:// prefix.
- New **Use for** setting; movies only by default.

## 0.1.2

- One API key field: the personal key from your fanart.tv profile. A key saved by 0.1.0/0.1.1 in the personal key field still works.

## 0.1.1

- Language and fallback language settings, both English by default.
- Accept the flat asset URLs Fanart.tv now serves, and its cz/ua/xx language codes.

## 0.1.0

- First release: posters, backgrounds, logos, clear art, banners, thumbs, discs and season posters from Fanart.tv.
