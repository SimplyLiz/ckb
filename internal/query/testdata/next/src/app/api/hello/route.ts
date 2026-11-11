import { greet } from "@/lib/used";

export async function GET() {
  return new Response(greet("api"));
}
