// The native core does not exist under Jest: tests that need it mock it.
jest.mock('./modules/kergui-core', () => ({
  __esModule: true,
  default: {
    version: () => 'test',
    request: jest.fn(),
    open: jest.fn(),
    configure: jest.fn(),
    certificateFingerprint: jest.fn(),
    journal: jest.fn(),
    note: jest.fn(),
    startDemo: jest.fn(),
    stopDemo: jest.fn(),
  },
}));
