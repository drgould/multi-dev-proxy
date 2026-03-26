import { headers } from "next/headers";
import { Counter } from "./counter";

export const dynamic = "force-dynamic";

export default async function Home() {
  const serverTime = new Date().toISOString();
  const headersList = await headers();
  const interestingHeaders: Record<string, string> = {};

  for (const key of ["host", "user-agent", "accept", "cookie", "x-forwarded-for", "x-forwarded-proto"]) {
    const val = headersList.get(key);
    if (val) interestingHeaders[key] = val;
  }

  return (
    <div
      style={{
        minHeight: "100vh",
        background: "linear-gradient(135deg, #1a0a2e 0%, #4a1d6e 100%)",
        color: "#e2d4f0",
        fontFamily: "'SF Mono', 'Fira Code', 'Cascadia Code', monospace",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        padding: "60px 20px 40px",
      }}
    >
      <h1
        style={{
          fontSize: "2.4rem",
          fontWeight: 700,
          margin: "0 0 8px",
          color: "#c8a2e8",
          letterSpacing: "-0.5px",
        }}
      >
        Next.js
      </h1>

      <p style={{ margin: "0 0 32px", opacity: 0.6, fontSize: "0.9rem" }}>
        Port <strong style={{ color: "#b07cd8" }}>{process.env.PORT || "3000"}</strong>
        &nbsp;&middot;&nbsp;Server-Side Rendered
      </p>

      {/* SSR Proof */}
      <div
        style={{
          background: "rgba(0,0,0,0.25)",
          border: "1px solid rgba(255,255,255,0.08)",
          borderRadius: "12px",
          padding: "24px 32px",
          marginBottom: "24px",
          minWidth: "300px",
          maxWidth: "560px",
          width: "100%",
        }}
      >
        <h2 style={{ margin: "0 0 16px", fontSize: "1.1rem", color: "#c8a2e8" }}>
          SSR Timestamps
        </h2>
        <div style={{ display: "flex", flexDirection: "column", gap: "8px", fontSize: "0.85rem" }}>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              padding: "8px 12px",
              background: "rgba(255,255,255,0.05)",
              borderRadius: "6px",
            }}
          >
            <span style={{ opacity: 0.6 }}>Server rendered at</span>
            <span style={{ color: "#b07cd8", fontWeight: 600 }}>{serverTime}</span>
          </div>
          <Counter />
        </div>
        <p
          style={{
            marginTop: "12px",
            fontSize: "0.75rem",
            opacity: 0.4,
            lineHeight: 1.5,
          }}
        >
          The server timestamp is fixed at render time. The client timestamp updates live.
          If they differ, SSR is working through the proxy.
        </p>
      </div>

      {/* Request Headers */}
      <div
        style={{
          background: "rgba(0,0,0,0.25)",
          border: "1px solid rgba(255,255,255,0.08)",
          borderRadius: "12px",
          padding: "24px 32px",
          marginBottom: "24px",
          minWidth: "300px",
          maxWidth: "560px",
          width: "100%",
        }}
      >
        <h2 style={{ margin: "0 0 16px", fontSize: "1.1rem", color: "#c8a2e8" }}>
          Request Headers <span style={{ fontSize: "0.75rem", opacity: 0.4 }}>(server component)</span>
        </h2>
        <div style={{ display: "flex", flexDirection: "column", gap: "4px", fontSize: "0.8rem" }}>
          {Object.entries(interestingHeaders).map(([key, value]) => (
            <div
              key={key}
              style={{
                display: "flex",
                gap: "12px",
                padding: "6px 12px",
                background: "rgba(255,255,255,0.05)",
                borderRadius: "6px",
                wordBreak: "break-all",
              }}
            >
              <span style={{ color: "#b07cd8", fontWeight: 600, minWidth: "130px", flexShrink: 0 }}>
                {key}
              </span>
              <span style={{ opacity: 0.7 }}>{value}</span>
            </div>
          ))}
          {Object.keys(interestingHeaders).length === 0 && (
            <p style={{ opacity: 0.4, fontStyle: "italic" }}>No headers captured</p>
          )}
        </div>
      </div>

      {/* API Route */}
      <div
        style={{
          background: "rgba(0,0,0,0.25)",
          border: "1px solid rgba(255,255,255,0.08)",
          borderRadius: "12px",
          padding: "24px 32px",
          marginBottom: "24px",
          minWidth: "300px",
          maxWidth: "560px",
          width: "100%",
          textAlign: "center",
        }}
      >
        <h2 style={{ margin: "0 0 12px", fontSize: "1.1rem", color: "#c8a2e8" }}>
          API Route
        </h2>
        <p style={{ fontSize: "0.85rem", opacity: 0.6, marginBottom: "12px" }}>
          Test the Next.js API route through the proxy:
        </p>
        <a
          href="/api/info"
          target="_blank"
          rel="noopener noreferrer"
          style={{
            display: "inline-block",
            padding: "10px 20px",
            borderRadius: "8px",
            background: "#7c3aed",
            color: "#fff",
            fontWeight: 600,
            fontFamily: "inherit",
            fontSize: "0.9rem",
            textDecoration: "none",
          }}
        >
          GET /api/info &rarr;
        </a>
      </div>

      {/* Footer */}
      <p
        style={{
          marginTop: "auto",
          paddingTop: "40px",
          fontSize: "0.8rem",
          opacity: 0.4,
          textAlign: "center",
          maxWidth: "400px",
          lineHeight: 1.5,
        }}
      >
        If mdp is working, you should see a floating switcher widget at the top of this page.
      </p>
    </div>
  );
}
