import { randomBytes, randomUUID } from "node:crypto";
import { writeFileSync, readFileSync, appendFileSync } from "node:fs";
try {
  writeFileSync(
    ".env",
    `# Generated for local development; never commit this file.\nPOSTGRES_PASSWORD=${randomBytes(24).toString("hex")}\nS3_ACCESS_KEY_ID=opencompute\nS3_SECRET_ACCESS_KEY=${randomBytes(24).toString("hex")}\nKEYCLOAK_ADMIN_PASSWORD=${randomBytes(24).toString("hex")}\nOPERATOR_ID=${randomUUID()}\nOPERATOR_NAME=OpenCompute\n`,
    { flag: "wx", mode: 0o600 },
  );
  console.log(
    "Created .env with unique local secrets. Use the Keycloak admin console to create users and assign roles.",
  );
} catch (e) {
  if (e.code === "EEXIST") console.log("Existing .env preserved");
  else throw e;
}

// Upgrade existing development configuration without replacing established secrets.
if (!/^SCHEDULER_CLIENT_SECRET=/m.test(readFileSync(".env", "utf8"))) {
  appendFileSync(
    ".env",
    `\nSCHEDULER_CLIENT_SECRET=${randomBytes(32).toString("hex")}\n`,
    { mode: 0o600 },
  );
  console.log("Added scheduler service credential; existing values preserved");
}
