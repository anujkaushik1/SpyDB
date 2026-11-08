// The app imports its own files without an extension ('./copyFormats');
// Vite finds the .ts file, Node needs telling.
export async function resolve(specifier, context, nextResolve) {
  const relative = specifier.startsWith('./') || specifier.startsWith('../')
  if (relative && !/\.[a-z]+$/i.test(specifier)) {
    try {
      return await nextResolve(`${specifier}.ts`, context)
    } catch {
      // not a .ts file next to it: resolve as written
    }
  }
  return nextResolve(specifier, context)
}
