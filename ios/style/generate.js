import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { LIGHT, layers } from "@protomaps/basemaps";

const TILE_URL = "https://spur-tiles.sasilver0051.workers.dev/tiles/{z}/{x}/{y}.mvt";
const GLYPHS_URL = "https://protomaps.github.io/basemaps-assets/fonts/{fontstack}/{range}.pbf";
const SPRITE_URL = "https://protomaps.github.io/basemaps-assets/sprites/v4/light";
const LANG = "en";

const here = path.dirname(fileURLToPath(import.meta.url));
const outPath = path.resolve(here, "../Spur/Spur/MapStyleLight.json");

const style = {
  version: 8,
  name: "Spur Light",
  sources: {
    protomaps: {
      type: "vector",
      tiles: [TILE_URL],
      minzoom: 0,
      maxzoom: 15,
      attribution:
        '<a href="https://github.com/protomaps/basemaps">Protomaps</a> © <a href="https://osm.org/copyright">OpenStreetMap</a>',
    },
  },
  glyphs: GLYPHS_URL,
  sprite: SPRITE_URL,
  layers: layers("protomaps", LIGHT, { lang: LANG }),
};

fs.writeFileSync(outPath, `${JSON.stringify(style, null, 2)}\n`);
console.log(`wrote ${outPath} (${style.layers.length} layers)`);
