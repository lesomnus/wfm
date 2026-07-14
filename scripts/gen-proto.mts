#!/usr/bin/env -S npm exec tsx
import "zx/globals";
import { rmSync } from "node:fs";
import { fileURLToPath } from "node:url";

declare global {
  var rootDir: string;
}

globalThis.rootDir = path.join(fileURLToPath(import.meta.url), '../..');

$.verbose = true
cd(rootDir)

{
	const generated_files = await glob([
		path.join(rootDir, "proto/**/*.g.proto"),
		path.join(rootDir, "proto.svc/**/*.g.proto"),
	]);
	for (const f of generated_files) {
		rmSync(f);
	}
}

await $`buf generate`;


const service_files = await glob([
	path.join(rootDir, "proto.svc/**/*_svc.g.proto"),
	path.join(rootDir, "proto.svc/**/*_svc.proto"),
]);
for (const f of service_files) {
	const d = path.dirname(f);

	let n = path.basename(f, ".proto");
	if (n.endsWith(".g")) {
		n = n.slice(0, -('.g'.length));
	}

	const p = path.join(d, `${n}.ext.proto`);

	const r = path.relative(path.join(rootDir, "proto.svc"), d);
	const v = path.join(path.join(rootDir, "proto", r), `${n}.g.proto`);
	if (fs.existsSync(p)) {
		const o = fs.createWriteStream(v);
		await $`go tool github.com/lesomnus/proto-merge ${f} ${p}`.pipe(o);
	} else {
		fs.copyFileSync(f, v);
	}
}

// Second pass: now that the merged service protos exist under proto/, run the
// generators again so the Go bindings (message + gRPC stubs) for the services
// are emitted. The first `buf generate` ran before these files existed.
await $`buf generate`;
