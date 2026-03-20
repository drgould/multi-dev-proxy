"use client";

import { useState, useEffect } from "react";

export function Counter() {
  const [count, setCount] = useState(0);
  const [clientTime, setClientTime] = useState<string | null>(null);

  useEffect(() => {
    setClientTime(new Date().toISOString());
    const interval = setInterval(() => {
      setClientTime(new Date().toISOString());
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  return (
    <>
      {/* Client timestamp */}
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          padding: "8px 12px",
          background: "rgba(255,255,255,0.05)",
          borderRadius: "6px",
        }}
      >
        <span style={{ opacity: 0.6 }}>Client time (live)</span>
        <span style={{ color: "#9f7aea", fontWeight: 600 }}>
          {clientTime ?? "hydrating..."}
        </span>
      </div>

      {/* Counter */}
      <div
        style={{
          marginTop: "20px",
          padding: "20px",
          background: "rgba(255,255,255,0.03)",
          borderRadius: "8px",
          textAlign: "center",
        }}
      >
        <p style={{ fontSize: "0.85rem", opacity: 0.5, marginBottom: "12px" }}>
          Client-side counter (proves JS hydration through proxy)
        </p>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: "16px" }}>
          <button
            type="button"
            onClick={() => setCount((c) => c - 1)}
            style={{
              width: "40px",
              height: "40px",
              borderRadius: "8px",
              border: "1px solid rgba(255,255,255,0.15)",
              background: "rgba(255,255,255,0.06)",
              color: "#e2d4f0",
              fontSize: "1.4rem",
              cursor: "pointer",
            }}
          >
            &minus;
          </button>
          <span style={{ fontSize: "2rem", fontWeight: 700, minWidth: "60px", color: "#b07cd8" }}>
            {count}
          </span>
          <button
            type="button"
            onClick={() => setCount((c) => c + 1)}
            style={{
              width: "40px",
              height: "40px",
              borderRadius: "8px",
              border: "1px solid rgba(255,255,255,0.15)",
              background: "rgba(255,255,255,0.06)",
              color: "#e2d4f0",
              fontSize: "1.4rem",
              cursor: "pointer",
            }}
          >
            +
          </button>
        </div>
      </div>
    </>
  );
}
