import { NextResponse } from "next/server";

export function GET() {
  return NextResponse.json({
    server: "c",
    framework: "next.js",
    port: process.env.PORT || "3000",
    time: new Date().toISOString(),
    ssr: true,
  });
}
