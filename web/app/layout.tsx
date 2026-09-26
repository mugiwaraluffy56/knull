import type { Metadata } from "next";
import Link from "next/link";
import { AuthStatus } from "@/components/AuthStatus";
import "./globals.css";

export const metadata: Metadata = {
  title: "Knull",
  description: "AI SRE — autonomous cloud operations agent",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>
        <header className="topbar">
          <nav className="topbar-nav">
            <Link href="/" className="brand" aria-label="Knull home"><span className="brand-mark" aria-hidden="true">✳</span>Knull</Link>
            <Link href="/fleet" className="navlink">
              Fleet
            </Link>
            <Link href="/services" className="navlink">
              Services
            </Link>
            <Link href="/integrations" className="navlink">
              Integrations
            </Link>
          </nav>
          <AuthStatus />
        </header>
        {children}
      </body>
    </html>
  );
}
