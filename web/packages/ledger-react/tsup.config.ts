import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { defineConfig } from "tsup";

const DIRECTIVES = ["use client", "use server"];
const DIRECTIVE_RE = /^\s*["'](use client|use server)["']\s*;?/;

interface Metafile {
  outputs: Record<string, { inputs: Record<string, unknown> }>;
}

/**
 * esbuild strips leading directives (e.g. "use client") when the directive
 * lives in an *imported* module rather than the bundle entry, which silently
 * breaks React Server Components boundaries. tsup runs esbuild plugins' onEnd
 * before writing files, so we patch the written output in `onSuccess` using the
 * build metafile: for each output chunk we inspect every source it bundled, and
 * if any source begins with a directive, re-prepend it to the chunk.
 *
 * A chunk that pulls in any "use client" source keeps the directive; chunks
 * built only from undirected sources stay undirected.
 */
async function preserveDirectives(distDir: string): Promise<void> {
  const metaPath = path.resolve(distDir, "metafile-esm.json");
  const meta = JSON.parse(await readFile(metaPath, "utf8")) as Metafile;
  const cwd = process.cwd();
  const directiveCache = new Map<string, string | null>();

  const directiveOf = async (input: string): Promise<string | null> => {
    const cached = directiveCache.get(input);
    if (cached !== undefined) return cached;
    let directive: string | null = null;
    try {
      const src = await readFile(path.resolve(cwd, input), "utf8");
      const match = DIRECTIVE_RE.exec(src)?.[1];
      if (match && DIRECTIVES.includes(match)) directive = match;
    } catch {
      directive = null;
    }
    directiveCache.set(input, directive);
    return directive;
  };

  await Promise.all(
    Object.entries(meta.outputs).map(async ([outPath, output]) => {
      if (!outPath.endsWith(".js")) return;
      let directive: string | null = null;
      for (const input of Object.keys(output.inputs)) {
        directive = await directiveOf(input);
        if (directive) break;
      }
      if (!directive) return;
      const abs = path.resolve(cwd, outPath);
      const text = await readFile(abs, "utf8");
      if (text.startsWith(`"${directive}"`)) return;
      await writeFile(abs, `"${directive}";\n${text}`);
    }),
  );
}

export default defineConfig({
  entry: ["src/index.ts", "src/server.ts"],
  format: ["esm"],
  dts: true,
  clean: true,
  splitting: true,
  metafile: true,
  external: [
    "react",
    "react-dom",
    "react/jsx-runtime",
    "@tanstack/react-query",
  ],
  async onSuccess() {
    await preserveDirectives("dist");
  },
});
