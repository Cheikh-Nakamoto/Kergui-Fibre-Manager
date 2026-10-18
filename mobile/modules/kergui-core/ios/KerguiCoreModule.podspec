Pod::Spec.new do |s|
  s.name           = 'KerguiCoreModule'
  s.version        = '0.1.0'
  s.summary        = 'Kergui Go core (gomobile) bridged to Expo'
  s.description    = 'Wraps KerguiCore.xcframework, built by `make mobile-core-ios`.'
  s.author         = 'Kergui Fibre Manager contributors'
  s.homepage       = 'https://github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager'
  s.license        = 'MIT'
  s.platforms      = { :ios => '16.4' }
  s.source         = { git: '' }
  s.static_framework = true

  s.dependency 'ExpoModulesCore'

  # Built by `make mobile-core-ios` (macOS) or the mobile-core workflow.
  s.vendored_frameworks = 'KerguiCore.xcframework'

  s.pod_target_xcconfig = {
    'DEFINES_MODULE' => 'YES',
  }

  s.source_files = "**/*.{h,m,mm,swift}"
end
