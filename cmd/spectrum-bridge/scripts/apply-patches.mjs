import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const target = path.resolve(__dirname, "../node_modules/@spectrum-ts/imessage/dist/index.js");
try {
  let text = fs.readFileSync(target, "utf8");
  const marker = 'log.info({ "spectrum.imessage.poll.guid": event.pollMessageGuid, deltaTitle: event.delta.title';
  if (text.includes(marker)) {
    console.log("imessage poll patch already applied");
    process.exit(0);
  }
  const old = "const cachePollEvent = (cache, event) => {\n\tif (event.delta.type === \"created\" || event.delta.type === \"optionAdded\") try {\n\t\tconst cached = toCachedPoll({";
  const neo = "const cachePollEvent = (cache, event) => {\n\tif (event.delta.type === \"created\" || event.delta.type === \"optionAdded\") try {\n\t\tlog.info({ \"spectrum.imessage.poll.guid\": event.pollMessageGuid, deltaTitle: event.delta.title, deltaOptions: event.delta.options?.map(o=>o.text), deltaType: event.delta.type }, \"caching poll event\");\n\t\tconst cached = toCachedPoll({";
  if (text.includes(old)) {
    text = text.replace(old, neo);
    fs.writeFileSync(target, text);
    console.log("patched imessage poll cache to log deltaTitle");
  } else {
    console.warn("patch target not found");
  }
} catch (e) {
  console.warn("patch failed (may be fresh install, will retry after npm install)", e.message);
}
