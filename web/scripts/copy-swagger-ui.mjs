import { cp, mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const source = fileURLToPath(new URL("../node_modules/swagger-ui-dist/", import.meta.url));
const destination = fileURLToPath(new URL("../public/swagger-ui/", import.meta.url));

await mkdir(destination, { recursive: true });
await cp(source, destination, { recursive: true });