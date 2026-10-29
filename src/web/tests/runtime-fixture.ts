import { execFileSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { createServer } from "node:net";
import { get } from "node:https";

// Disposable tests never write original passwords to files, argv, env or logs.
export async function startFixture() {
  const name = "sc-browser-" + randomBytes(5).toString("hex");
  const volume = name + "-data";
  const docker = (args: string[], input?: string) =>
    execFileSync("docker", args, {
      input,
      encoding: "utf8",
      stdio: ["pipe", "pipe", "pipe"],
    }).trim();
  const image = process.env.KRAKEN_TEST_IMAGE || "kraken:dev";
  const password = randomBytes(24).toString("base64url");
  const username = "admin";
  const cleanup = () => {
    try {
      docker(["rm", "-f", "-v", name]);
    } catch {}
    try {
      docker(["volume", "rm", volume]);
    } catch {}
  };
  try {
    const port = await new Promise<number>((resolve, reject) => {
      const server = createServer();
      server.on("error", reject);
      server.listen(0, "127.0.0.1", () => {
        const address = server.address();
        const p = typeof address === "object" && address ? address.port : 0;
        server.close((e) => (e ? reject(e) : resolve(p)));
      });
    });
    const origin = `https://localhost:${port}`;
    docker(["volume", "create", volume]);
    const oneShot = [
      "run",
      "--rm",
      "-i",
      "--user",
      "10013:10013",
      "-v",
      `${volume}:/data`,
      "--entrypoint",
      "kraken",
      image,
    ];
    docker([...oneShot, "dev-cert"]);
    docker([...oneShot, "bootstrap", "--password-stdin"], password + "\n");
    docker([
      "run",
      "-d",
      "--name",
      name,
      "-p",
      `127.0.0.1:${port}:8443`,
      "-v",
      `${volume}:/data`,
      "-e",
      `KRAKEN_PUBLIC_ORIGIN=${origin}`,
      image,
    ]);
    let ready = false;
    for (let attempt = 0; attempt < 100; attempt++) {
      ready = await new Promise<boolean>((resolve) => {
        const request = get(
          origin + "/healthz",
          { rejectUnauthorized: false },
          (response) => {
            response.resume();
            resolve(response.statusCode === 200);
          },
        );
        request.on("error", () => resolve(false));
        request.setTimeout(1000, () => request.destroy());
      });
      if (ready) break;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    if (!ready) throw new Error("Fixture panel did not become ready");
    return { name, volume, origin, username, password, docker, cleanup };
  } catch (error) {
    cleanup();
    throw new Error(String(error).replaceAll(password, "[redacted]"));
  }
}
