import { PMTiles, Source, RangeResponse } from "pmtiles";

interface Env {
  SPUR_TILES: R2Bucket;
  PMTILES_KEY: string;
}

class R2Source implements Source {
  constructor(
    private bucket: R2Bucket,
    private key: string,
  ) {}

  getKey(): string {
    return this.key;
  }

  async getBytes(offset: number, length: number): Promise<RangeResponse> {
    const obj = await this.bucket.get(this.key, {
      range: { offset, length },
    });
    if (!obj) {
      throw new Error(`PMTiles archive not found at key ${this.key}`);
    }
    return {
      data: await obj.arrayBuffer(),
      etag: obj.httpEtag,
    };
  }
}

const TILE_PATH = /^\/tiles\/(\d+)\/(\d+)\/(\d+)\.(?:mvt|pbf)$/;

const CACHE_HEADERS = {
  "Cache-Control": "public, max-age=86400, s-maxage=2592000, immutable",
  "Access-Control-Allow-Origin": "*",
};

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/healthz") {
      return new Response("ok", { headers: { "Cache-Control": "no-store" } });
    }

    const match = url.pathname.match(TILE_PATH);
    if (!match) {
      return new Response("Not found", { status: 404 });
    }

    const [, zStr, xStr, yStr] = match;
    const z = Number(zStr);
    const x = Number(xStr);
    const y = Number(yStr);

    const source = new R2Source(env.SPUR_TILES, env.PMTILES_KEY);
    const archive = new PMTiles(source);
    const tile = await archive.getZxy(z, x, y);

    if (!tile) {
      return new Response(null, { status: 204, headers: CACHE_HEADERS });
    }

    return new Response(tile.data, {
      headers: {
        "Content-Type": "application/vnd.mapbox-vector-tile",
        ...CACHE_HEADERS,
      },
    });
  },
} satisfies ExportedHandler<Env>;
