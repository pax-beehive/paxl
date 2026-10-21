async function geminiCore() {
  try {
    return await load("@google/gemini-cli-core");
  } catch (error) {
    if (
      error?.code !== "MODULE_NOT_FOUND" &&
      error?.code !== "ERR_MODULE_NOT_FOUND" &&
      error?.code !== "ERR_PACKAGE_PATH_NOT_EXPORTED"
    )
      throw error;
  }
  // Follow the selected CLI's declared re-export, not a stale hashed bundle.
  const entry = await fs.readFile(join(root, "bundle", "gemini.js"), "utf8");
  const match = entry.match(/import\(["']\.\/(dist-[A-Za-z0-9_-]+\.js)["']\)/);
  if (!match)
    throw new InputError(
      "The selected Gemini installation has no supported core SDK export. Upgrade Gemini.",
    );
  return import(pathToFileURL(join(root, "bundle", match[1])).href);
}
async function geminiSettings(sdk, type) {
  const path = sdk.Storage.getGlobalSettingsPath();
  let value = {};
  try {
    value = JSON.parse(await fs.readFile(path, "utf8"));
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new InputError("Gemini settings are not an object.");
  value.security ??= {};
  value.security.auth ??= {};
  if (type === undefined) delete value.security.auth.selectedType;
  else value.security.auth.selectedType = type;
  await fs.mkdir(dirname(path), { recursive: true, mode: 0o700 });
  // Use the native atomic write utility when available. Configuration contains
  // only the selected auth type; SDK functions own all secret storage.
  const target = await fs.realpath(path).catch((error) => {
    if (error.code === "ENOENT") return path;
    throw error;
  });
  const temp = target + ".paxl-" + process.pid;
  try {
    await fs.writeFile(temp, JSON.stringify(value, null, 2) + "\n", {
      mode: 0o600,
      flag: "wx",
    });
    await fs.rename(temp, target);
  } finally {
    await fs.unlink(temp).catch(() => {});
  }
}
async function runGemini() {
  const sdk = await geminiCore();
  for (const key of [
    "AuthType",
    "Storage",
    "getOauthClient",
    "loadApiKey",
    "saveApiKey",
    "clearCachedCredentialFile",
  ])
    if (!sdk[key])
      throw new InputError(
        "Selected Gemini SDK lacks " + key + ". Upgrade Gemini.",
      );
  const choices = [
    { id: "browser", label: "Google browser sign-in" },
    { id: "device", label: "Google manual authorization code" },
    { id: "api-key", label: "Gemini API key" },
    { id: "vertex", label: "Existing Vertex AI environment/ADC" },
  ];
  if (operation === "methods") {
    result({
      state: "available",
      providers: [
        { id: "google", label: "Gemini", methods: choices.map((x) => x.id) },
      ],
    });
    return;
  }
  if (requestedProvider && requestedProvider !== "google")
    throw new InputError("Gemini supports provider google; omit --provider.");
  if (operation === "status") {
    const providers = [];
    if (await sdk.loadApiKey())
      providers.push({
        id: "google",
        credential_type: "api-key",
        source: "native-store",
      });
    const cached = await fs.stat(sdk.Storage.getOAuthCredsPath()).then(
      (s) => s.isFile(),
      (e) => {
        if (e.code === "ENOENT") return false;
        throw e;
      },
    );
    if (cached)
      providers.push({
        id: "google",
        credential_type: "oauth",
        source: "native-store",
      });
    if (process.env.GEMINI_API_KEY)
      providers.push({
        id: "google",
        credential_type: "api-key",
        source: "environment",
      });
    if (process.env.GOOGLE_GENAI_USE_VERTEXAI === "true")
      providers.push({
        id: "google",
        credential_type: "vertex",
        source: "environment",
      });
    result({
      state: providers.length ? "configured" : "unknown",
      providers,
      message:
        metadata +
        " Encrypted OAuth keyring state is not inferred from a missing file.",
    });
    return;
  }
  if (operation === "logout") {
    await sdk.saveApiKey(null);
    await sdk.clearCachedCredentialFile();
    if (await sdk.loadApiKey())
      throw new InputError(
        "Gemini still reports a stored API key after removal.",
      );
    await geminiSettings(sdk, undefined);
    result({
      state: "succeeded",
      message:
        "Native cached OAuth and API-key removal requested. Environment/ADC and other application credentials are unchanged.",
    });
    return;
  }
  const method = requestedMethod || "browser";
  if (!choices.some((x) => x.id === method))
    throw new InputError(
      "Supported Gemini methods: " + choices.map((x) => x.id).join(", "),
    );
  if (method === "api-key") {
    const key = await question("Gemini API key: ", true);
    await sdk.saveApiKey(key);
    if ((await sdk.loadApiKey()) !== key)
      throw new InputError("Gemini did not retain the supplied API key.");
    await geminiSettings(sdk, sdk.AuthType.USE_GEMINI);
  } else if (method === "vertex") {
    if (!process.env.GOOGLE_CLOUD_PROJECT || !process.env.GOOGLE_CLOUD_LOCATION)
      throw new InputError(
        "Vertex requires GOOGLE_CLOUD_PROJECT, GOOGLE_CLOUD_LOCATION and existing ADC or GOOGLE_API_KEY.",
      );
    await geminiSettings(sdk, sdk.AuthType.USE_VERTEX_AI);
    result({
      state: "configured",
      method,
      message:
        "Selected native Vertex auth. Existing ADC/environment credentials and model access are not verified.",
    });
    return;
  } else {
    if (method === "device" && !process.stdin.isTTY)
      throw new InputError(
        "Google manual-code login requires an interactive terminal. Use browser login locally.",
      );
    const feedback = (value) => write(String(value?.message ?? "") + "\n");
    sdk.coreEvents?.on("user-feedback", feedback);
    try {
      await sdk.getOauthClient(sdk.AuthType.LOGIN_WITH_GOOGLE, {
        getProxy: () => process.env.HTTPS_PROXY || process.env.HTTP_PROXY,
        isBrowserLaunchSuppressed: () =>
          method === "device" ||
          ["true", "1"].includes(process.env.NO_BROWSER?.toLowerCase()),
        isInteractive: () => Boolean(process.stdin.isTTY),
        getAcpMode: () => true,
      });
      await geminiSettings(sdk, sdk.AuthType.LOGIN_WITH_GOOGLE);
    } finally {
      sdk.coreEvents?.off("user-feedback", feedback);
    }
  }
  result({
    state: "succeeded",
    provider: "google",
    method,
    message:
      "Native authentication completed; model entitlement is not verified.",
  });
}
await finish(runGemini);
