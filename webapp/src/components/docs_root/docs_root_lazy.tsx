// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {Suspense} from 'react';

// The Docs product UI ("backstage") and its whole import tree are code-split
// into their own async chunk, so the plugin's initial bundle stays small and
// the UI loads only when the product mounts. webpack fetches the chunk at
// runtime; output.publicPath: 'auto' resolves it relative to the served plugin
// bundle (no hardcoded plugin static path).
const DocsRoot = React.lazy(() => import(/* webpackChunkName: "docs-backstage" */ './docs_root'));

const DocsRootLazy = () => (
    <Suspense fallback={null}>
        <DocsRoot/>
    </Suspense>
);

export default DocsRootLazy;
