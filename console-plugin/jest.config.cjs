module.exports = {
  preset: 'ts-jest',
  testEnvironment: 'node',
  // e2e/ holds the unit-tested config loaders; the Playwright *.spec.ts files
  // there are run by `bun run test-e2e` against a live console, not by jest.
  roots: [
    '<rootDir>/src',
    '<rootDir>/tools/attribution',
    '<rootDir>/tools/size',
    '<rootDir>/e2e',
  ],
  testPathIgnorePatterns: ['/node_modules/', '\\.spec\\.ts$'],
};
