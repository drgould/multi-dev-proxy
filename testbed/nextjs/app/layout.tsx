import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Server C — Next.js",
  description: "mdp testbed server C",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
