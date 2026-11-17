import type { Metadata } from "next";
import "./style.css";
export const metadata: Metadata = {
  title: "OpenCompute · Company Platform",
  description: "Your organization’s independent compute network",
};
export default function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
