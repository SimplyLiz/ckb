export function greet(name: string): string {
  return `hello ${shout(name)}`;
}

export function shout(name: string): string {
  return name.toUpperCase();
}

export const LIMITS = { max: 3, min: 1 };
