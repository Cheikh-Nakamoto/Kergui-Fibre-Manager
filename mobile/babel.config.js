const { readFlavor } = require('./config/licensing');

// The build flavor (config/licensing.js) is inlined as a literal wherever the
// app reads `process.env.KERGUI_FLAVOR`. In a release bundle Metro then folds
// `process.env.KERGUI_FLAVOR === 'naboopay'` to false and drops the branch,
// with the require inside it: the play bundle carries no NabooPay code.
function inlineFlavor(flavor) {
  return ({ types: t }) => ({
    name: 'kergui-inline-flavor',
    visitor: {
      MemberExpression(path) {
        if (path.matchesPattern('process.env.KERGUI_FLAVOR') && !path.parentPath.isAssignmentExpression({ left: path.node })) {
          path.replaceWith(t.stringLiteral(flavor));
        }
      },
    },
  });
}

module.exports = function (api) {
  const flavor = readFlavor(process.env);
  api.cache.using(() => flavor);
  return {
    presets: ['babel-preset-expo'],
    plugins: [inlineFlavor(flavor)],
  };
};
