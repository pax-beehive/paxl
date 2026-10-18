async function runDsh() {
  const { Context } = await load("@deepseek-ai/cordis");
  const ctx = new Context();
  try {
    await ctx.plugin(
      (await load("@deepseek-ai/dsh-credentials-local")).default,
      { watch: false },
    );
    await ctx.plugin((await load("@deepseek-ai/dsh-authorization")).default);
    await ctx.plugin((await load("@deepseek-ai/dsh-llm")).default);
    await ctx.plugin(await load("@deepseek-ai/dsh-llm-pi-ai"), {
      providers: {},
    });
    const accountModule = await load(
      "@deepseek-ai/dsh-deepseek-account-platform",
    );
    // Account service initialization can migrate a stale issuer record; status and
    // discovery instead describe the native record without mounting that owner.
    const accountKey = "deepseek-account-platform/default";
    const flows = ctx.authorization
      .list()
      .map((f) => ({ ...f, id: f.key.split("/")[1] }));
    const entries = [
      {
        id: "deepseek-account",
        key: accountKey,
        label: "DeepSeek account",
        methods: [{ id: "browser", label: "Browser account login" }],
      },
      ...flows,
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
    if (operation === "status") {
      const stored = [];
      for (const e of entries) {
        if (requestedProvider && requestedProvider !== e.id) continue;
        const info = await ctx.credentials.describeRecord(e.key);
        if (info.configured)
          stored.push({
            id: e.id,
            credential_type: info.kind,
            source: "stored",
          });
      }
      if (requestedProvider && !entries.some((e) => e.id === requestedProvider))
        throw new InputError(
          "Unknown DSH provider. Available: " +
            entries.map((e) => e.id).join(", "),
        );
      result({
        state: stored.length ? "configured" : "unknown",
        providers: stored,
        message: metadata,
      });
      return;
    }
    const id = requestedProvider || (await choose("Provider", entries));
    const entry = entries.find((e) => e.id === id);
    if (!entry)
      throw new InputError(
        "Unknown DSH provider. Available: " +
          entries.map((e) => e.id).join(", "),
      );
    if (id === "deepseek-account") {
      if (
        operation === "login" &&
        requestedMethod &&
        requestedMethod !== "browser"
      )
        throw new InputError("DeepSeek account supports browser.");
      if (operation === "login")
        await ctx.plugin(
          (await load("@deepseek-ai/dsh-host-webserver")).default,
          { host: "127.0.0.1", port: 0 },
        );
      await ctx.plugin(accountModule.default);
      const service = ctx.deepseekAccount;
      if (operation === "logout") {
        await service.signOut();
        result({
          state: "succeeded",
          provider: id,
          message:
            "Removed the native account grant. Environment credentials may remain.",
        });
        return;
      }
      const started = await service.startSignIn(
        "en",
        `http://127.0.0.1:${ctx.webServer.port}`,
        "desktop",
      );
      const attempt = started.attempt;
      if (!attempt) throw new InputError("Native account login did not start.");
      const cancel = () => {
        void service.cancelSignIn(attempt.id);
      };
      controller.signal.addEventListener("abort", cancel, { once: true });
      try {
        let printed = false;
        for await (const view of service.watch(controller.signal)) {
          if (view.attempt?.id !== attempt.id)
            throw new InputError("Native account login was replaced.");
          if (view.attempt.authorizeUrl && !printed) {
            notice({
              message: "Open this URL in your browser:",
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
            result({ state: "succeeded", provider: id });
            return;
          }
        }
        throw new InputError("Native account login ended without success.");
      } finally {
        controller.signal.removeEventListener("abort", cancel);
      }
    }
    if (operation === "logout") {
      await ctx.credentials.deleteRecord(entry.key);
      result({
        state: "succeeded",
        provider: id,
        message:
          "Removed the selected native credential record. Environment and reference credentials may remain.",
      });
      return;
    }
    const method =
      requestedMethod ||
      (entry.methods.length === 1
        ? entry.methods[0].id
        : await choose("Method", entry.methods));
    if (!entry.methods.some((m) => m.id === method))
      throw new InputError(
        `Unsupported method. Supported methods for ${id}: ${entry.methods.map((m) => m.id).join(", ")}`,
      );
    const outcome = await ctx.authorization.begin({
      key: entry.key,
      method,
      signal: controller.signal,
      interaction: { notify: notice, prompt: nativePrompt },
    });
    if (outcome.status !== "authorized")
      throw new InputError("Native authorization was cancelled.");
    result({ state: "succeeded", provider: id, method });
  } finally {
    await ctx.fiber.dispose();
  }
}
await finish(runDsh);
