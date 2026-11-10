export function wired(): number { return helper(); }
function helper(): number { return 1; }
export function unwiredFn(): number { return 2; }
export const arrowUnwired = () => 3;
function privateUnused(): number { return 4; }
export class Box { open(): void {} }
