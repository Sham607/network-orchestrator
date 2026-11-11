import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function delay(ms: number): Promise<void> {
  const pAny = Promise as unknown as { withResolvers?: <T>() => { promise: Promise<T>; resolve: (val?: T) => void } }
  if (typeof pAny.withResolvers === 'function') {
    const { promise, resolve } = pAny.withResolvers<void>()
    setTimeout(resolve, ms)
    return promise
  }
  return new Promise((resolve) => setTimeout(resolve, ms))
}
