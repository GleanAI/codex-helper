import { readdir, stat } from "node:fs/promises";
import { resolve } from "node:path";

const assets = resolve("../backend/internal/web/dist/assets");
const files = (await readdir(assets)).filter((file) => file.endsWith(".js"));
const limit = 500_000;
const oversized = [];
for (const file of files) {
  const size = (await stat(resolve(assets, file))).size;
  if (size > limit) oversized.push(`${file}: ${size} bytes`);
}
if (oversized.length) {
  throw new Error(
    `JavaScript bundle 超过 ${limit} bytes:\n${oversized.join("\n")}`,
  );
}
