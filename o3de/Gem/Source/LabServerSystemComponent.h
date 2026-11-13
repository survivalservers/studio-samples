#pragma once

#include <AzCore/Component/Component.h>
#include <AzCore/Component/TickBus.h>
#include <AzCore/std/smart_ptr/unique_ptr.h>

#include <LabServer/LabServerBus.h>

#include "LabGame.h"

namespace LabServer
{
    // The project's system component, as the MinimalProject template makes it, plus the sample server: in a server
    // launcher it runs LabGame (LabGame.h) on the game's tick and ends the launcher's main loop when the game is done.
    // In the game launcher, the editor and the asset tools it does nothing.
    class LabServerSystemComponent
        : public AZ::Component
        , protected LabServerRequestBus::Handler
        , public AZ::TickBus::Handler
    {
    public:
        AZ_COMPONENT_DECL(LabServerSystemComponent);

        static void Reflect(AZ::ReflectContext* context);

        static void GetProvidedServices(AZ::ComponentDescriptor::DependencyArrayType& provided);
        static void GetIncompatibleServices(AZ::ComponentDescriptor::DependencyArrayType& incompatible);
        static void GetRequiredServices(AZ::ComponentDescriptor::DependencyArrayType& required);
        static void GetDependentServices(AZ::ComponentDescriptor::DependencyArrayType& dependent);

        LabServerSystemComponent();
        ~LabServerSystemComponent();

    protected:
        void Init() override;
        void Activate() override;
        void Deactivate() override;

        // AZ::TickBus
        void OnTick(float deltaTime, AZ::ScriptTimePoint time) override;

    private:
        AZStd::unique_ptr<LabGame> m_game;
        bool m_exitAsked = false;
    };
}
