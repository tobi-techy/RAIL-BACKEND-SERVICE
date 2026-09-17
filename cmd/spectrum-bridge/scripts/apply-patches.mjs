import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
const here = path.dirname(fileURLToPath(import.meta.url));

// Find the imessage dist file relative to this script regardless of where
// npm runs postinstall from (repo root vs cmd/spectrum-bridge).
const candidates = [
  path.resolve(here, "../node_modules/@spectrum-ts/imessage/dist/index.js"),
  path.resolve(here, "../../node_modules/@spectrum-ts/imessage/dist/index.js"),
];
const target = candidates.find((p) => fs.existsSync(p));
if (!target) {
  console.warn("spectrum-ts imessage dist not found; skipping patch");
  process.exit(0);
}

function patch(targetFile, old, neo, marker) {
  let text = fs.readFileSync(targetFile, "utf8");
  if (text.includes(marker)) return "already applied";
  if (!text.includes(old)) return "target not found";
  fs.writeFileSync(targetFile, text.replace(old, neo));
  return "patched";
}

try {
  // 1. Log the Apple event delta title before Zod rejects it, so we can see
  //    whether the server-side poll metadata has an empty title.
  const r1 = patch(
    target,
    "const cachePollEvent = (cache, event) => {\n\tif (event.delta.type === \"created\" || event.delta.type === \"optionAdded\") try {\n\t\tconst cached = toCachedPoll({",
    "const cachePollEvent = (cache, event) => {\n\tif (event.delta.type === \"created\" || event.delta.type === \"optionAdded\") try {\n\t\tlog.info({ \"spectrum.imessage.poll.guid\": event.pollMessageGuid, deltaTitle: event.delta.title, deltaOptions: event.delta.options?.map(o=>o.text), deltaType: event.delta.type }, \"caching poll event\");\n\t\tconst cached = toCachedPoll({",
    "log.info({ \"spectrum.imessage.poll.guid\": event.pollMessageGuid, deltaTitle: event.delta.title"
  );
  console.log("cachePollEvent log:", r1);

  // 2. Coerce empty server-side poll titles so Zod never rejects the cache.
  //    The server (photon PollCreated.title / PollInfo.title) drops the title
  //    even though our poll(title, options) call sends it correctly. Without
  //    this, every poll fails to cache and every vote fails to resolve.
  const r2 = patch(
    target,
    "const toCachedPoll = (input) => {\n\tconst poll = asPoll({\n\t\ttitle: input.title,\n\t\toptions: input.options.map((optionInfo) => ({ title: optionInfo.text }))\n\t});",
    "const toCachedPoll = (input) => {\n\tconst cachedTitle = (typeof input.title === \"string\" ? input.title : \"\").trim();\n\tconst safeTitle = cachedTitle || \"(poll)\";\n\tconst poll = asPoll({\n\t\ttitle: safeTitle,\n\t\toptions: input.options.map((optionInfo) => ({ title: optionInfo.text }))\n\t});",
    "const cachedTitle = (typeof input.title === \"string\" ? input.title : \"\").trim()"
  );
  console.log("toCachedPoll coerce:", r2);
} catch (e) {
  console.warn("patch failed", e.message);
}