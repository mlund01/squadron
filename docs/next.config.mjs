import withMarkdoc from '@markdoc/next.js';

/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'export',
  basePath: '/squadron',
  pageExtensions: ['md', 'mdoc', 'js', 'jsx', 'ts', 'tsx'],
};

export default withMarkdoc({
  schemaPath: './markdoc',
})(nextConfig);
