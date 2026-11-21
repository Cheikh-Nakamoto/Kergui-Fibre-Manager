// Learn more https://docs.expo.io/guides/customizing-metro
const { getDefaultConfig } = require('expo/metro-config');

const { readFlavor } = require('./config/licensing');

/** @type {import('expo/metro-config').MetroConfig} */
const config = getDefaultConfig(__dirname);

// babel.config.js inlines the flavor into the code, so a transform cached
// for one flavor must never be reused for the other.
config.cacheVersion = `${config.cacheVersion ?? ''}flavor:${readFlavor(process.env)}`;

module.exports = config;
