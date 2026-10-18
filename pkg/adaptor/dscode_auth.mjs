async function runDscode() {
  const home =
    process.env.DSCODE_HOME ||
    join(homedir(), ".local", "share", "dscode-state");
  if (!home.startsWith("/") && process.platform !== "win32")
    throw new InputError("DSCODE_HOME must be absolute.");
  process.env.DSH_HOME = home; // The official standalone wrapper uses this private state root too.
  const { Context } = await load("@deepseek-ai/cordis");
  const plugin = (name) =>
    import(pathToFileURL(join(root, "plugins", name, "index.mjs")).href);
  const ctx = new Context();
  try {
    await ctx.plugin((await plugin("credentials")).default, {
      dshHome: home,
      path: join(home, "credentials.yaml"),
      watch: false,
    });
    await ctx.plugin((await load("@deepseek-ai/dsh-authorization")).default);
    await ctx.plugin((await load("@deepseek-ai/dsh-llm")).default);
    for (const name of ["openai", "opencode-go", "antigravity"])
      await ctx.plugin(await plugin(name));
    const catalog = (
      await import(
        pathToFileURL(join(root, "plugins", "providers", "catalog.mjs")).href
      )
    ).PROVIDERS;
    const entries = [
      {
        id: "deepseek-account",
        label: "DeepSeek account",
        methods: [{ id: "browser" }],
      },
      ...catalog
        .filter((p) => p.id !== "grok")
        .map((p) => ({
          id: p.id,
          label: p.name,
          ref: p.credentialRef,
          methods:
            p.id === "openai"
              ? [{ id: "oauth" }, { id: "api-key" }]
              : p.login
                ? [{ id: "oauth" }]
                : [{ id: "api-key" }],
        })),
    ];
    if (operation === "methods") {
      result({
        state: "available",
        providers: entries.map((e) => ({
          id: e.id,
          label: e.label,
          methods: e.methods.map((m) => m.id),
        })),
      });
      return;
    }
    if (requestedProvider && !entries.some((e) => e.id === requestedProvider))
      throw new InputError(
        "Unknown DSCODE provider. Available: " +
          entries.map((e) => e.id).join(", "),
      );
    if (operation === "status") {
      const providers = [];
      for (const entry of entries) {
        if (requestedProvider && requestedProvider !== entry.id) continue;
        const info =
          entry.id === "deepseek-account"
            ? await ctx.credentials.describeRecord(
                "deepseek-account-platform/default",
              )
            : await ctx.credentials.describe(entry.ref);
        if (info.configured)
          providers.push({
            id: entry.id,
            credential_type: info.kind || info.source || "configured",
            source: info.source || "stored",
          });
      }
      result({
        state: providers.length ? "configured" : "unknown",
        providers,
        message: metadata,
      });
      return;
    }
    const id = requestedProvider || (await choose("Provider", entries)),
      entry = entries.find((e) => e.id === id);
    if (!entry) throw new InputError("Unknown DSCODE provider.");
    const method =
      operation === "login"
        ? requestedMethod ||
          (entry.methods.length === 1
            ? entry.methods[0].id
            : await choose("Method", entry.methods))
        : undefined;
    if (operation === "login" && !entry.methods.some((m) => m.id === method))
      throw new InputError(
        `Supported methods for ${id}: ${entry.methods.map((m) => m.id).join(", ")}`,
      );
    if (id === "deepseek-account") {
      if (operation === "login")
        await ctx.plugin(
          (await load("@deepseek-ai/dsh-host-webserver")).default,
          { host: "127.0.0.1", port: 0 },
        );
      await ctx.plugin(
        (await load("@deepseek-ai/dsh-deepseek-account-platform")).default,
      );
      const service = ctx.deepseekAccount;
      if (operation === "logout") {
        await service.signOut();
        result({ state: "succeeded", provider: id });
        return;
      }
      const started = await service.startSignIn(
          "en",
          `http://127.0.0.1:${ctx.webServer.port}`,
          "desktop",
        ),
        attempt = started.attempt;
      if (!attempt)
        throw new InputError("Native DeepSeek account login did not start.");
      const cancel = () => {
        void service.cancelSignIn(attempt.id);
      };
      controller.signal.addEventListener("abort", cancel, { once: true });
      try {
        let printed = false;
        for await (const view of service.watch(controller.signal)) {
          if (view.attempt?.id !== attempt.id)
            throw new InputError("Native account attempt was replaced.");
          if (view.attempt.authorizeUrl && !printed) {
            notice({
              message: "Open this URL:",
              url: view.attempt.authorizeUrl,
            });
            printed = true;
          }
          if (
            ["succeeded", "failed", "expired", "cancelled"].includes(
              view.attempt.phase,
            )
          ) {
            if (view.attempt.phase !== "succeeded")
              throw new InputError("Native account login did not complete.");
            result({ state: "succeeded", provider: id, method });
            return;
          }
        }
      } finally {
        controller.signal.removeEventListener("abort", cancel);
      }
      throw new InputError("Native account login ended without success.");
    }
    const login =
      id === "openai"
        ? ctx.dscodeOpenAI.login
        : id === "opencode-go"
          ? ctx.dscodeOpenCodeLogin
          : id === "antigravity"
            ? ctx.dscodeAntigravityLogin
            : undefined;
    if (operation === "logout") {
      if (login) {
        if (id === "openai") await login.auth.logout();
        else await login.logout("en");
      }
      if (entry.ref && !["opencode-go", "antigravity"].includes(id))
        await ctx.credentials.unset(entry.ref);
      result({
        state: "succeeded",
        provider: id,
        message:
          "Removed selected native provider credentials. Environment and other application credentials remain.",
      });
      return;
    }
    if (method === "api-key") {
      if (
        id !== "openai" &&
        !(await ctx.credentials.describe(entry.ref)).writable
      )
        throw new InputError(
          "This credential is controlled by the launching environment or account mode; change that source first.",
        );
      const key = await question("API key: ", true);
      await ctx.credentials.set(entry.ref, key);
      if (id === "openai") await login.auth.select("api-key");
      result({ state: "succeeded", provider: id, method });
      return;
    }
    if (!login)
      throw new InputError("Selected native login service is unavailable.");
    // Observe the owner's successful commit, not just an old credential that
    // was already present when a failed/replaced OAuth attempt began.
    const owner =
      id === "openai"
        ? login.auth
        : id === "opencode-go"
          ? login.deps.store
          : login.session;
    const key = id === "opencode-go" ? "write" : "save",
      original = owner[key].bind(owner);
    let committed = false;
    owner[key] = async (...args) => {
      const value = await original(...args);
      if (id === "opencode-go" || value === true) committed = true;
      return value;
    };
    const cancel = () => login.cancel("en");
    controller.signal.addEventListener("abort", cancel, { once: true });
    try {
      const output =
        id === "openai" ? await login.login() : await login.login("en");
      if (Array.isArray(output?.lines))
        output.lines.forEach((line) => write(line + "\n"));
      else write(String(output) + "\n");
      const attempt = login.attempt;
      if (attempt?.done) await attempt.done;
      if (!committed)
        throw new InputError("Native login did not commit a new credential.");
      result({ state: "succeeded", provider: id, method });
    } finally {
      owner[key] = original;
      controller.signal.removeEventListener("abort", cancel);
    }
  } finally {
    await ctx.fiber.dispose();
  }
}
await finish(runDscode);
