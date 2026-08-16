// src/app/layout.tsx — GopherProxy Dashboard root layout
import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "GopherProxy Dashboard — Observability",
  description:
    "Real-time observability dashboard for the GopherProxy reverse proxy. " +
    "Shows backend health, request throughput, rate limiting, and latency metrics from Prometheus.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
