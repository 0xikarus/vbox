import { defineRailway, preserve, project, service } from "railway/iac";

// Last resort for a per-service CaC repo. Prefer one .railway file for the
// project and drop this if you later combine services into that file.
export const partial = "vmbox-controller";

export default defineRailway(() => {
  const vmbox_controller = service("vmbox-controller", {
    start: "/usr/local/bin/vmbox-controller",
    healthcheck: "/healthz",
    healthcheckTimeout: 300,
    env: {
      DATABASE_URL: preserve(),
      VMBOX_ACCOUNT_NAME: preserve(),
      VMBOX_BOOTSTRAP_TOKEN_HASH: preserve(),
      VMBOX_CONTROLLER_URL: preserve(),
      VMBOX_ENCRYPTION_KEY: preserve(),
      VMBOX_OWNER_SUBJECT: preserve(),
    },
    // dockerfilePath from CaC: "Dockerfile"
    // builder from CaC: "DOCKERFILE"
  });
  return project("spacevm", {
    resources: [vmbox_controller],
  });
});
