import { nodes as defaultNodes, Tag } from '@markdoc/markdoc';

export const fence = {
  ...defaultNodes.fence,
  transform(node, config) {
    const attributes = node.transformAttributes(config);
    const children = node.transformChildren(config);
    const language = node.attributes.language || 'text';

    return new Tag(
      'pre',
      { className: `language-${language}` },
      [
        new Tag(
          'code',
          { className: `language-${language}` },
          [node.attributes.content]
        )
      ]
    );
  },
};
