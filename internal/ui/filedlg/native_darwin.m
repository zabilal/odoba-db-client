//go:build cgo

#import <Cocoa/Cocoa.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <stdint.h>

extern void filedlgDone(uintptr_t h, char *path);

// filedlgShow opens a save or open panel: a sheet on win, or a window of its
// own when win is 0. The strings are copied before it returns. The panel
// answers through filedlgDone with the path, or NULL for a cancel.
void filedlgShow(uintptr_t h, uintptr_t win, int save, const char *message, const char *name,
	const char *exts, const char *dir, const char *accept) {
	@autoreleasepool {
		NSString *msg = [NSString stringWithUTF8String:message];
		NSString *nm = [NSString stringWithUTF8String:name];
		NSString *ex = [NSString stringWithUTF8String:exts];
		NSString *dr = [NSString stringWithUTF8String:dir];
		NSString *acc = [NSString stringWithUTF8String:accept];

		void (^open)(void) = ^{
			NSSavePanel *panel = save ? [NSSavePanel savePanel] : [NSOpenPanel openPanel];
			if (!save) {
				NSOpenPanel *op = (NSOpenPanel *)panel;
				op.canChooseFiles = YES;
				op.canChooseDirectories = NO;
				op.allowsMultipleSelection = NO;
			}
			panel.canCreateDirectories = YES;
			if (msg.length > 0) {
				panel.message = msg;
			}
			if (save && nm.length > 0) {
				panel.nameFieldStringValue = nm;
			}
			if (dr.length > 0) {
				panel.directoryURL = [NSURL fileURLWithPath:dr isDirectory:YES];
			}
			if (acc.length > 0) {
				panel.prompt = acc;
			}
			if (ex.length > 0) {
				NSMutableArray<UTType *> *types = [NSMutableArray array];
				for (NSString *e in [ex componentsSeparatedByString:@"\n"]) {
					UTType *t = [UTType typeWithFilenameExtension:e];
					if (t != nil) {
						[types addObject:t];
					}
				}
				if (types.count > 0) {
					panel.allowedContentTypes = types;
				}
			}
			void (^finish)(NSModalResponse) = ^(NSModalResponse r) {
				NSURL *u = nil;
				if (r == NSModalResponseOK) {
					u = save ? panel.URL : ((NSOpenPanel *)panel).URLs.firstObject;
				}
				filedlgDone(h, u != nil ? (char *)u.fileSystemRepresentation : NULL);
			};
			NSWindow *w = (NSWindow *)(void *)win;
			if (w != nil) {
				[panel beginSheetModalForWindow:w completionHandler:finish];
			} else {
				[panel beginWithCompletionHandler:finish];
			}
		};
		if ([NSThread isMainThread]) {
			open();
		} else {
			dispatch_async(dispatch_get_main_queue(), open);
		}
	}
}
