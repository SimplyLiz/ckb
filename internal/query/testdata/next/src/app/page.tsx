import { greet } from "@/lib/used";

export const metadata = { title: "Home" };
export const dynamic = "force-dynamic";

export default function Home() {
  return <main>{greet("world")}</main>;
}
